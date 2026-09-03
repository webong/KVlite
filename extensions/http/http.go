// Package kvlitehttp adds KVLite's optional JSON/HTTP server and Go remote
// client. Importing the embeddable kvlite core alone never starts a listener.
package kvlitehttp

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/webong/kvlite"
)

const defaultMaxRequestBytes int64 = 64 << 20

const driverHeader = "X-KVLite-Driver"

func init() {
	kvlite.MustRegisterLinkedModule(Manifest())
}

// Manifest describes the optional HTTP transport. An ordinary import of the
// core never registers this module or opens a network listener; importing this
// package only makes its explicit Serve API available.
func Manifest() kvlite.ModuleManifest {
	return kvlite.ModuleManifest{
		SchemaVersion: kvlite.ModuleManifestVersion,
		Name:          "http",
		Kind:          kvlite.ModuleKindExtension,
		Version:       "v0.1.0",
		ModuleABI:     kvlite.ModuleABIVersion,
		Capabilities:  []string{"http-client", "http-server", "remote-driver-selection"},
		License:       "Apache-2.0",
	}
}

// Options configures the optional local HTTP transport. DriverPaths exposes
// additional server-owned directories under their selected drivers. RedisURL
// advertises an independently started Redis extension for the primary
// database only. A remote client may choose one of the mapped driver names
// with X-KVLite-Driver, but it never supplies a filesystem path.
type Options struct {
	ListenAddress   string
	BearerToken     string
	MaxRequestBytes int64
	DriverPaths     map[kvlite.DriverName]string
	RedisURL        string
}

// Server owns an optional HTTP listener and any additional server-owned
// driver/path mappings. Closing a Server does not close its primary DB.
type Server struct {
	listener  net.Listener
	server    *http.Server
	databases *sharedDatabases
}

type scanItem struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type sharedDatabases struct {
	defaultDriver kvlite.DriverName
	databases     map[kvlite.DriverName]*kvlite.DB
	auxiliary     []*kvlite.DB
}

type driverRouteError struct {
	code      string
	driver    kvlite.DriverName
	available []kvlite.DriverName
	err       error
}

func (err *driverRouteError) Error() string {
	if err.err != nil {
		return err.err.Error()
	}
	return "kvlite: requested driver is not available from this server"
}

func (err *driverRouteError) Unwrap() error { return err.err }

func newSharedDatabases(primary *kvlite.DB, options Options) (*sharedDatabases, error) {
	primaryDriver := kvlite.DriverName(primary.Backend())
	result := &sharedDatabases{
		defaultDriver: primaryDriver,
		databases:     map[kvlite.DriverName]*kvlite.DB{primaryDriver: primary},
	}
	for requestedDriver, path := range options.DriverPaths {
		driver, err := kvlite.ParseDriverName(string(requestedDriver))
		if err != nil {
			result.closeAuxiliary()
			return nil, err
		}
		if path == "" {
			result.closeAuxiliary()
			return nil, fmt.Errorf("%w: a path is required for shared driver %q", kvlite.ErrInvalidArgument, driver)
		}
		if _, exists := result.databases[driver]; exists {
			result.closeAuxiliary()
			return nil, fmt.Errorf("%w: shared driver %q is already mapped", kvlite.ErrInvalidArgument, driver)
		}

		auxiliary, err := kvlite.Open(path, kvlite.WithDriver(string(driver)))
		if err != nil {
			result.closeAuxiliary()
			return nil, err
		}
		result.databases[driver] = auxiliary
		result.auxiliary = append(result.auxiliary, auxiliary)
	}
	return result, nil
}

func (databases *sharedDatabases) closeAuxiliary() error {
	var closeErr error
	for _, database := range databases.auxiliary {
		if err := database.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	databases.auxiliary = nil
	return closeErr
}

func (databases *sharedDatabases) driverNames() []kvlite.DriverName {
	names := make([]kvlite.DriverName, 0, len(databases.databases))
	for name := range databases.databases {
		names = append(names, name)
	}
	sort.Slice(names, func(left, right int) bool { return names[left] < names[right] })
	return names
}

func (databases *sharedDatabases) driverInfos() []kvlite.DriverInfo {
	names := databases.driverNames()
	infos := make([]kvlite.DriverInfo, 0, len(names))
	for _, name := range names {
		info, err := kvlite.DriverInfoFor(name)
		if err != nil {
			continue
		}
		infos = append(infos, info)
	}
	return infos
}

func (databases *sharedDatabases) redisURL(database *kvlite.DB, configuredURL string) string {
	if kvlite.DriverName(database.Backend()) != databases.defaultDriver {
		return ""
	}
	return configuredURL
}

func (databases *sharedDatabases) resolve(request *http.Request) (*kvlite.DB, kvlite.DriverName, error) {
	raw := request.Header.Get(driverHeader)
	driver := databases.defaultDriver
	if raw != "" {
		canonical, err := kvlite.ParseDriverName(raw)
		if err != nil {
			return nil, "", &driverRouteError{code: "invalid_driver", driver: kvlite.DriverName(raw), available: databases.driverNames(), err: err}
		}
		driver = canonical
	}
	database, found := databases.databases[driver]
	if found {
		return database, driver, nil
	}
	info, err := kvlite.DriverInfoFor(driver)
	if errors.Is(err, kvlite.ErrDriverNotInstalled) {
		return nil, driver, &driverRouteError{code: "driver_not_installed", driver: driver, available: databases.driverNames(), err: err}
	}
	if err != nil {
		return nil, driver, &driverRouteError{code: "invalid_driver", driver: driver, available: databases.driverNames(), err: err}
	}
	if !info.Available {
		return nil, driver, &driverRouteError{
			code:      "driver_unavailable",
			driver:    driver,
			available: databases.driverNames(),
			err:       fmt.Errorf("%w: driver %q cannot run in this server", kvlite.ErrDriverUnavailable, driver),
		}
	}
	return nil, driver, &driverRouteError{
		code:      "driver_not_exposed",
		driver:    driver,
		available: databases.driverNames(),
		err:       fmt.Errorf("%w: driver %q is installed but this server has no mapped database for it", kvlite.ErrDriverNotExposed, driver),
	}
}

func resolveSharedDatabase(databases *sharedDatabases, w http.ResponseWriter, request *http.Request) (*kvlite.DB, bool) {
	database, driver, err := databases.resolve(request)
	if err != nil {
		writeDriverRouteError(w, err)
		return nil, false
	}
	w.Header().Set(driverHeader, string(driver))
	return database, true
}

func writeDriverRouteError(w http.ResponseWriter, err error) {
	status := http.StatusConflict
	payload := struct {
		Error struct {
			Code             string              `json:"code"`
			Driver           kvlite.DriverName   `json:"driver,omitempty"`
			AvailableDrivers []kvlite.DriverName `json:"available_drivers,omitempty"`
			Message          string              `json:"message"`
		} `json:"error"`
	}{}
	payload.Error.Code = "driver_unavailable"
	payload.Error.Message = err.Error()
	var routeError *driverRouteError
	if errors.As(err, &routeError) {
		payload.Error.Code = routeError.code
		payload.Error.Driver = routeError.driver
		payload.Error.AvailableDrivers = routeError.available
		if routeError.code == "driver_not_installed" {
			status = http.StatusNotImplemented
		} else if routeError.code == "invalid_driver" {
			status = http.StatusBadRequest
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// Serve starts an explicit KVLite HTTP server. The supplied DB remains owned
// by the caller and is not closed when Server.Close is called.
func Serve(db *kvlite.DB, options Options) (*Server, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: database is required", kvlite.ErrInvalidArgument)
	}
	if db.Backend() == kvlite.BackendRemote {
		return nil, fmt.Errorf("%w: a remote database cannot own an HTTP server", kvlite.ErrInvalidArgument)
	}
	if options.ListenAddress == "" {
		options.ListenAddress = "127.0.0.1:0"
	}
	databases, err := newSharedDatabases(db, options)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", options.ListenAddress)
	if err != nil {
		_ = databases.closeAuxiliary()
		return nil, fmt.Errorf("kvlite: start sharing endpoint: %w", err)
	}
	if options.MaxRequestBytes <= 0 {
		options.MaxRequestBytes = defaultMaxRequestBytes
	}
	mux := http.NewServeMux()
	server := &http.Server{
		Handler:           sharingAuth(options.BearerToken, mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	result := &Server{listener: listener, server: server, databases: databases}
	mux.HandleFunc("GET /v1", func(w http.ResponseWriter, request *http.Request) {
		database, ok := resolveSharedDatabase(databases, w, request)
		if !ok {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Protocol string              `json:"protocol"`
			Entries  string              `json:"entries"`
			Methods  []string            `json:"methods"`
			Driver   kvlite.DriverName   `json:"driver"`
			Backend  kvlite.Backend      `json:"backend"`
			Drivers  []kvlite.DriverInfo `json:"drivers"`
			Redis    string              `json:"redis,omitempty"`
		}{
			Protocol: "kvlite/1",
			Entries:  "/v1/entries/{base64url-key}",
			Methods:  []string{"GET", "PUT", "DELETE"},
			Driver:   kvlite.DriverName(database.Backend()),
			Backend:  database.Backend(),
			Drivers:  databases.driverInfos(),
			Redis:    databases.redisURL(database, options.RedisURL),
		})
	})
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, request *http.Request) {
		_, ok := resolveSharedDatabase(databases, w, request)
		if !ok {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("/v1/entries/{key}", func(w http.ResponseWriter, request *http.Request) {
		database, ok := resolveSharedDatabase(databases, w, request)
		if !ok {
			return
		}
		handleJSONEntry(database, options.MaxRequestBytes, w, request)
	})
	mux.HandleFunc("/v1/kv/{key}", func(w http.ResponseWriter, request *http.Request) {
		database, ok := resolveSharedDatabase(databases, w, request)
		if !ok {
			return
		}
		key, err := base64.RawURLEncoding.DecodeString(request.PathValue("key"))
		if err != nil {
			http.Error(w, "invalid key", http.StatusBadRequest)
			return
		}
		switch request.Method {
		case http.MethodGet:
			value, found, err := database.Transport().Get(request.Context(), key)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if !found {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(value)
		case http.MethodPut:
			request.Body = http.MaxBytesReader(w, request.Body, options.MaxRequestBytes)
			value, err := io.ReadAll(request.Body)
			if err != nil {
				http.Error(w, "invalid or oversized body", http.StatusRequestEntityTooLarge)
				return
			}
			if err := database.Transport().Put(request.Context(), key, value); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodDelete:
			if err := database.Transport().Delete(request.Context(), key); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.Header().Set("Allow", "GET, PUT, DELETE")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("GET /v1/scan", func(w http.ResponseWriter, request *http.Request) {
		database, ok := resolveSharedDatabase(databases, w, request)
		if !ok {
			return
		}
		prefix, err := base64.RawURLEncoding.DecodeString(request.URL.Query().Get("prefix"))
		if err != nil {
			http.Error(w, "invalid prefix", http.StatusBadRequest)
			return
		}
		items := make([]scanItem, 0)
		err = database.Transport().ScanPrefix(request.Context(), prefix, func(key, value []byte) error {
			items = append(items, scanItem{
				Key:   base64.RawStdEncoding.EncodeToString(key),
				Value: base64.RawStdEncoding.EncodeToString(value),
			})
			return nil
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(items)
	})
	mux.HandleFunc("POST /v1/list/{key}/push", func(w http.ResponseWriter, request *http.Request) {
		database, ok := resolveSharedDatabase(databases, w, request)
		if !ok {
			return
		}
		key, err := base64.RawURLEncoding.DecodeString(request.PathValue("key"))
		if err != nil {
			http.Error(w, "invalid key", http.StatusBadRequest)
			return
		}
		request.Body = http.MaxBytesReader(w, request.Body, options.MaxRequestBytes)
		data, err := io.ReadAll(request.Body)
		if err != nil {
			http.Error(w, "invalid or oversized body", http.StatusRequestEntityTooLarge)
			return
		}
		newItems, err := decodeList(data)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		length, err := database.Transport().PushList(request.Context(), key, newItems, request.URL.Query().Get("left") == "1")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Length int `json:"length"`
		}{Length: length})
	})
	go func() {
		_ = server.Serve(listener)
	}()
	return result, nil
}

// handleJSONEntry is the language-neutral API. It deliberately accepts and
// returns JSON values, while /v1/kv remains the internal envelope transport
// used by the Go remote client.
func handleJSONEntry(db *kvlite.DB, maxRequestBytes int64, w http.ResponseWriter, request *http.Request) {
	rawKey, err := base64.RawURLEncoding.DecodeString(request.PathValue("key"))
	if err != nil || len(rawKey) == 0 {
		http.Error(w, "invalid key", http.StatusBadRequest)
		return
	}
	key := string(rawKey)
	ctx := request.Context()
	switch request.Method {
	case http.MethodGet:
		value, err := db.GetStoredValue(ctx, key)
		if errors.Is(err, kvlite.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "invalid stored value", http.StatusInternalServerError)
			return
		}
		if value.Codec != (kvlite.JSONCodec{}).Name() {
			http.Error(w, "stored value is not JSON", http.StatusUnsupportedMediaType)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if value.ExpiresAt > 0 {
			w.Header().Set("X-KVLite-Expires-At", strconv.FormatInt(value.ExpiresAt, 10))
		}
		_, _ = w.Write(value.Payload)
	case http.MethodPut:
		request.Body = http.MaxBytesReader(w, request.Body, maxRequestBytes)
		payload, err := io.ReadAll(request.Body)
		if err != nil || !json.Valid(payload) {
			http.Error(w, "request body must be valid JSON and within the size limit", http.StatusBadRequest)
			return
		}
		putOptions, err := parseTTLQuery(request.URL.Query().Get("ttl_seconds"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := db.PutStoredValue(ctx, key, (kvlite.JSONCodec{}).Name(), payload, putOptions...); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		if err := db.Delete(ctx, key); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func parseTTLQuery(raw string) ([]kvlite.PutOption, error) {
	if raw == "" {
		return nil, nil
	}
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || seconds < 0 {
		return nil, fmt.Errorf("ttl_seconds must be a non-negative integer")
	}
	if seconds == 0 {
		return nil, nil
	}
	if seconds > int64(^uint64(0)>>1)/int64(time.Second) {
		return nil, fmt.Errorf("ttl_seconds is too large")
	}
	return []kvlite.PutOption{kvlite.TTL(time.Duration(seconds) * time.Second)}, nil
}

func sharingAuth(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	expected := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		actual := []byte(request.Header.Get("Authorization"))
		if len(actual) != len(expected) || subtle.ConstantTimeCompare(actual, expected) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, request)
	})
}

// URL returns the base URL clients should use to connect to this server.
func (server *Server) URL() string {
	return "http://" + server.listener.Addr().String()
}

// Close stops the HTTP listener and closes any auxiliary driver/path mappings.
// It does not close the primary DB passed to Serve.
func (server *Server) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := server.server.Shutdown(ctx)
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	if server.databases != nil {
		if closeErr := server.databases.closeAuxiliary(); closeErr != nil && err == nil {
			err = closeErr
		}
	}
	return err
}

// ClientOptions configures an HTTP connection to a KVLite server. Driver is a
// server-side selection only: it names one server-owned mapping and never
// identifies a local filesystem path.
type ClientOptions struct {
	BearerToken string
	HTTPClient  *http.Client
	Driver      kvlite.DriverName
}

// Connect opens a Go DB handle backed by KVLite's optional HTTP transport.
// The database's storage still belongs to the server. dbOptions configure
// local decoding behavior (for example WithRegisteredCodec); select a remote
// driver through ClientOptions.Driver rather than WithDriver.
func Connect(baseURL string, remoteOptions ClientOptions, dbOptions ...kvlite.Option) (*kvlite.DB, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("%w: valid remote http(s) URL is required", kvlite.ErrInvalidArgument)
	}
	client := remoteOptions.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	selected := kvlite.BackendRemote
	requestedDriver := remoteOptions.Driver
	if requestedDriver != "" {
		requestedDriver, err = kvlite.ParseDriverName(string(requestedDriver))
		if err != nil {
			return nil, err
		}
		selected = kvlite.Backend(requestedDriver)
	}
	engine := &remoteEngine{baseURL: parsed.String(), token: remoteOptions.BearerToken, client: client, driver: requestedDriver}
	if requestedDriver != "" {
		// An explicit driver is part of the connection contract, not merely a
		// preference for the first operation. Validate it immediately so callers
		// get the server's structured driver error from Connect.
		response, err := engine.request(context.Background(), http.MethodGet, "/v1", nil)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, remoteStatusError(response)
		}
	}
	return kvlite.OpenWithEngine(engine, selected, dbOptions...)
}

// OpenRemote is an explicit alias for Connect.
func OpenRemote(baseURL string, options ClientOptions, dbOptions ...kvlite.Option) (*kvlite.DB, error) {
	return Connect(baseURL, options, dbOptions...)
}

type remoteEngine struct {
	baseURL string
	token   string
	client  *http.Client
	driver  kvlite.DriverName
}

// RemoteDriverError is returned when a remote server cannot honour an explicit
// X-KVLite-Driver selection. Callers can use errors.Is with the corresponding
// kvlite driver error.
type RemoteDriverError struct {
	StatusCode       int
	Code             string
	Driver           kvlite.DriverName
	AvailableDrivers []kvlite.DriverName
	Message          string
}

func (err *RemoteDriverError) Error() string {
	if err.Message != "" {
		return fmt.Sprintf("kvlite: remote driver %q (%s): %s", err.Driver, err.Code, err.Message)
	}
	return fmt.Sprintf("kvlite: remote driver %q failed with %s", err.Driver, err.Code)
}

func (err *RemoteDriverError) Unwrap() error {
	switch err.Code {
	case "driver_not_installed":
		return kvlite.ErrDriverNotInstalled
	case "driver_unavailable":
		return kvlite.ErrDriverUnavailable
	case "driver_not_exposed":
		return kvlite.ErrDriverNotExposed
	case "invalid_driver":
		return kvlite.ErrInvalidArgument
	default:
		return nil
	}
}

func (engine *remoteEngine) request(ctx context.Context, method, endpoint string, body io.Reader) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, engine.baseURL+endpoint, body)
	if err != nil {
		return nil, err
	}
	if engine.token != "" {
		request.Header.Set("Authorization", "Bearer "+engine.token)
	}
	if engine.driver != "" {
		request.Header.Set("X-KVLite-Driver", string(engine.driver))
	}
	return engine.client.Do(request)
}

func remoteKeyPath(key []byte) string {
	return "/v1/kv/" + base64.RawURLEncoding.EncodeToString(key)
}

func (engine *remoteEngine) Get(ctx context.Context, key []byte) ([]byte, bool, error) {
	response, err := engine.request(ctx, http.MethodGet, remoteKeyPath(key), nil)
	if err != nil {
		return nil, false, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}
	if response.StatusCode != http.StatusOK {
		return nil, false, remoteStatusError(response)
	}
	data, err := io.ReadAll(response.Body)
	return data, err == nil, err
}

func (engine *remoteEngine) Put(ctx context.Context, key, value []byte) error {
	response, err := engine.request(ctx, http.MethodPut, remoteKeyPath(key), bytes.NewReader(value))
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return remoteStatusError(response)
	}
	return nil
}

func (engine *remoteEngine) Delete(ctx context.Context, key []byte) error {
	response, err := engine.request(ctx, http.MethodDelete, remoteKeyPath(key), nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return remoteStatusError(response)
	}
	return nil
}

func (engine *remoteEngine) ScanPrefix(ctx context.Context, prefix []byte, callback func(key, value []byte) error) error {
	endpoint := "/v1/scan?prefix=" + url.QueryEscape(base64.RawURLEncoding.EncodeToString(prefix))
	response, err := engine.request(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return remoteStatusError(response)
	}
	var items []scanItem
	if err := json.NewDecoder(response.Body).Decode(&items); err != nil {
		return err
	}
	for _, item := range items {
		key, err := base64.RawStdEncoding.DecodeString(item.Key)
		if err != nil {
			return err
		}
		value, err := base64.RawStdEncoding.DecodeString(item.Value)
		if err != nil {
			return err
		}
		if err := callback(key, value); err != nil {
			return err
		}
	}
	return nil
}

func (engine *remoteEngine) PushList(ctx context.Context, key []byte, items [][]byte, left bool) (int, error) {
	endpoint := "/v1/list/" + base64.RawURLEncoding.EncodeToString(key) + "/push"
	if left {
		endpoint += "?left=1"
	}
	response, err := engine.request(ctx, http.MethodPost, endpoint, bytes.NewReader(encodeList(items)))
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, remoteStatusError(response)
	}
	var result struct {
		Length int `json:"length"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return 0, err
	}
	return result.Length, nil
}

func encodeList(items [][]byte) []byte {
	size := 4
	for _, item := range items {
		size += 4 + len(item)
	}
	result := make([]byte, size)
	binary.BigEndian.PutUint32(result[:4], uint32(len(items)))
	offset := 4
	for _, item := range items {
		binary.BigEndian.PutUint32(result[offset:], uint32(len(item)))
		offset += 4
		copy(result[offset:], item)
		offset += len(item)
	}
	return result
}

func decodeList(data []byte) ([][]byte, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("kvlite: invalid list encoding")
	}
	count := int(binary.BigEndian.Uint32(data[:4]))
	if count > (len(data)-4)/4 {
		return nil, fmt.Errorf("kvlite: invalid list item count")
	}
	items := make([][]byte, 0, count)
	offset := 4
	for index := 0; index < count; index++ {
		if offset+4 > len(data) {
			return nil, fmt.Errorf("kvlite: truncated list encoding")
		}
		length := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		offset += 4
		if length < 0 || offset+length > len(data) {
			return nil, fmt.Errorf("kvlite: truncated list item")
		}
		items = append(items, append([]byte(nil), data[offset:offset+length]...))
		offset += length
	}
	if offset != len(data) {
		return nil, fmt.Errorf("kvlite: trailing list data")
	}
	return items, nil
}

func remoteStatusError(response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
	message := strings.TrimSpace(string(body))
	var payload struct {
		Error struct {
			Code             string              `json:"code"`
			Driver           kvlite.DriverName   `json:"driver"`
			AvailableDrivers []kvlite.DriverName `json:"available_drivers"`
			Message          string              `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &payload) == nil && payload.Error.Code != "" {
		if payload.Error.Message == "" {
			payload.Error.Message = message
		}
		return &RemoteDriverError{
			StatusCode:       response.StatusCode,
			Code:             payload.Error.Code,
			Driver:           payload.Error.Driver,
			AvailableDrivers: payload.Error.AvailableDrivers,
			Message:          payload.Error.Message,
		}
	}
	return fmt.Errorf("kvlite: remote returned %s: %s", response.Status, message)
}

func (engine *remoteEngine) Close() error {
	if transport, ok := engine.client.Transport.(interface{ CloseIdleConnections() }); ok {
		transport.CloseIdleConnections()
	}
	return nil
}
