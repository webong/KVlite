package kvlite

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
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
)

const defaultMaxRequestBytes int64 = 64 << 20

const driverHeader = "X-KVLite-Driver"

// SharingOptions configures the optional local HTTP transport. DriverPaths
// exposes additional server-owned directories under their selected drivers.
// A remote client may choose one of these names with X-KVLite-Driver, but it
// never supplies a filesystem path.
type SharingOptions struct {
	ListenAddress   string
	BearerToken     string
	MaxRequestBytes int64
	DriverPaths     map[DriverName]string
}

type shareServer struct {
	listener  net.Listener
	server    *http.Server
	databases *sharedDatabases
}

type scanItem struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type sharedDatabases struct {
	defaultDriver DriverName
	databases     map[DriverName]*DB
	auxiliary     []*DB
}

type driverRouteError struct {
	code      string
	driver    DriverName
	available []DriverName
	err       error
}

func (err *driverRouteError) Error() string {
	if err.err != nil {
		return err.err.Error()
	}
	return "kvlite: requested driver is not available from this server"
}

func (err *driverRouteError) Unwrap() error { return err.err }

func newSharedDatabases(primary *DB, options SharingOptions) (*sharedDatabases, error) {
	primaryDriver := DriverName(primary.Backend())
	result := &sharedDatabases{
		defaultDriver: primaryDriver,
		databases:     map[DriverName]*DB{primaryDriver: primary},
	}
	for requestedDriver, path := range options.DriverPaths {
		driver, err := normalizeDriverName(requestedDriver)
		if err != nil {
			result.closeAuxiliary()
			return nil, err
		}
		if path == "" {
			result.closeAuxiliary()
			return nil, fmt.Errorf("%w: a path is required for shared driver %q", ErrInvalidArgument, driver)
		}
		if _, exists := result.databases[driver]; exists {
			result.closeAuxiliary()
			return nil, fmt.Errorf("%w: shared driver %q is already mapped", ErrInvalidArgument, driver)
		}

		cfg := primary.cfg
		cfg.driver = driver
		cfg.driverExplicit = true
		cfg.sharing = nil
		cfg.redis = nil
		storage, backend, err := openConfiguredEngine(path, cfg)
		if err != nil {
			result.closeAuxiliary()
			return nil, err
		}
		auxiliary, err := newDB(storage, cfg, backend)
		if err != nil {
			_ = storage.Close()
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

func (databases *sharedDatabases) driverNames() []DriverName {
	names := make([]DriverName, 0, len(databases.databases))
	for name := range databases.databases {
		names = append(names, name)
	}
	sort.Slice(names, func(left, right int) bool { return names[left] < names[right] })
	return names
}

func (databases *sharedDatabases) driverInfos() []DriverInfo {
	names := databases.driverNames()
	infos := make([]DriverInfo, 0, len(names))
	for _, name := range names {
		info, err := DriverInfoFor(name)
		if err != nil {
			continue
		}
		infos = append(infos, info)
	}
	return infos
}

func (databases *sharedDatabases) resolve(request *http.Request) (*DB, DriverName, error) {
	raw := request.Header.Get(driverHeader)
	driver := databases.defaultDriver
	if raw != "" {
		canonical, err := normalizeDriverName(DriverName(raw))
		if err != nil {
			return nil, "", &driverRouteError{code: "invalid_driver", driver: DriverName(raw), available: databases.driverNames(), err: err}
		}
		driver = canonical
	}
	database, found := databases.databases[driver]
	if found {
		return database, driver, nil
	}
	_, registered, err := registeredDriverFor(driver)
	if errors.Is(err, ErrDriverNotInstalled) {
		return nil, driver, &driverRouteError{code: "driver_not_installed", driver: driver, available: databases.driverNames(), err: err}
	}
	if err != nil {
		return nil, driver, &driverRouteError{code: "invalid_driver", driver: driver, available: databases.driverNames(), err: err}
	}
	if availabilityErr := registered.driver.Available(); availabilityErr != nil {
		return nil, driver, &driverRouteError{
			code:      "driver_unavailable",
			driver:    driver,
			available: databases.driverNames(),
			err:       fmt.Errorf("%w: driver %q cannot run in this server: %v", ErrDriverUnavailable, driver, availabilityErr),
		}
	}
	return nil, driver, &driverRouteError{
		code:      "driver_not_exposed",
		driver:    driver,
		available: databases.driverNames(),
		err:       fmt.Errorf("%w: driver %q is installed but this server has no mapped database for it", ErrDriverNotExposed, driver),
	}
}

func resolveSharedDatabase(databases *sharedDatabases, w http.ResponseWriter, request *http.Request) (*DB, bool) {
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
			Code             string       `json:"code"`
			Driver           DriverName   `json:"driver,omitempty"`
			AvailableDrivers []DriverName `json:"available_drivers,omitempty"`
			Message          string       `json:"message"`
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

func startShareServer(db *DB, options SharingOptions) (*shareServer, error) {
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
	result := &shareServer{listener: listener, server: server, databases: databases}
	mux.HandleFunc("GET /v1", func(w http.ResponseWriter, request *http.Request) {
		database, ok := resolveSharedDatabase(databases, w, request)
		if !ok {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Protocol string       `json:"protocol"`
			Entries  string       `json:"entries"`
			Methods  []string     `json:"methods"`
			Driver   DriverName   `json:"driver"`
			Backend  Backend      `json:"backend"`
			Drivers  []DriverInfo `json:"drivers"`
			Redis    string       `json:"redis,omitempty"`
		}{
			Protocol: "kvlite/1",
			Entries:  "/v1/entries/{base64url-key}",
			Methods:  []string{"GET", "PUT", "DELETE"},
			Driver:   DriverName(database.Backend()),
			Backend:  database.Backend(),
			Drivers:  databases.driverInfos(),
			Redis:    database.RedisAddress(),
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
			value, found, err := database.engine.Get(request.Context(), key)
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
			if err := database.engine.Put(request.Context(), key, value); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodDelete:
			if err := database.engine.Delete(request.Context(), key); err != nil {
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
		err = database.engine.ScanPrefix(request.Context(), prefix, func(key, value []byte) error {
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
		database.collectionMu.Lock()
		defer database.collectionMu.Unlock()
		items, err := database.readList(request.Context(), key)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if request.URL.Query().Get("left") == "1" {
			items = append(newItems, items...)
		} else {
			items = append(items, newItems...)
		}
		if err := database.writeList(request.Context(), key, items); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Length int `json:"length"`
		}{Length: len(items)})
	})
	go func() {
		_ = server.Serve(listener)
	}()
	return result, nil
}

// handleJSONEntry is the language-neutral API. It deliberately accepts and
// returns JSON values, while /v1/kv remains the internal envelope transport
// used by the Go remote client.
func handleJSONEntry(db *DB, maxRequestBytes int64, w http.ResponseWriter, request *http.Request) {
	rawKey, err := base64.RawURLEncoding.DecodeString(request.PathValue("key"))
	if err != nil || len(rawKey) == 0 {
		http.Error(w, "invalid key", http.StatusBadRequest)
		return
	}
	key := string(rawKey)
	ctx := request.Context()
	switch request.Method {
	case http.MethodGet:
		data, found, err := db.engine.Get(ctx, valueKey(key))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !found {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		value, err := unmarshalEnvelope(data)
		if err != nil {
			http.Error(w, "invalid stored value", http.StatusInternalServerError)
			return
		}
		if value.expiresAt > 0 && db.cfg.now().UnixNano() >= value.expiresAt {
			_ = db.engine.Delete(ctx, valueKey(key))
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if value.codec != (JSONCodec{}).Name() {
			http.Error(w, "stored value is not JSON", http.StatusUnsupportedMediaType)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if value.expiresAt > 0 {
			w.Header().Set("X-KVLite-Expires-At", strconv.FormatInt(value.expiresAt, 10))
		}
		_, _ = w.Write(value.payload)
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
		if err := db.Put(ctx, key, json.RawMessage(payload), putOptions...); err != nil {
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

func parseTTLQuery(raw string) ([]PutOption, error) {
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
	return []PutOption{TTL(time.Duration(seconds) * time.Second)}, nil
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

func (server *shareServer) address() string {
	return "http://" + server.listener.Addr().String()
}

func (server *shareServer) close() error {
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

// RemoteOptions configures a connection to a shareable KVLite database.
type RemoteOptions struct {
	BearerToken string
	HTTPClient  *http.Client
}

// OpenRemote connects to an owner process created with WithSharing.
func OpenRemote(baseURL string, remoteOptions RemoteOptions, options ...Option) (*DB, error) {
	cfg, err := buildConfig(options)
	if err != nil {
		return nil, err
	}
	if cfg.sharing != nil {
		return nil, fmt.Errorf("%w: a remote connection cannot host a sharing endpoint", ErrInvalidArgument)
	}
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("%w: valid remote http(s) URL is required", ErrInvalidArgument)
	}
	client := remoteOptions.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	selected := BackendRemote
	requestedDriver := DriverName("")
	if cfg.driverExplicit {
		selected = Backend(cfg.driver)
		requestedDriver = cfg.driver
	}
	engine := &remoteEngine{baseURL: parsed.String(), token: remoteOptions.BearerToken, client: client, driver: requestedDriver}
	if requestedDriver != "" {
		// An explicit driver is part of the connection contract, not merely a
		// preference for the first operation. Validate it immediately so callers
		// get the server's structured driver error from OpenRemote.
		response, err := engine.request(context.Background(), http.MethodGet, "/v1", nil)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, remoteStatusError(response)
		}
	}
	return newDB(engine, cfg, selected)
}

type remoteEngine struct {
	baseURL string
	token   string
	client  *http.Client
	driver  DriverName
}

// RemoteDriverError is returned when a remote server cannot honour an explicit
// X-KVLite-Driver selection. Callers can use errors.Is with
// ErrDriverNotInstalled, ErrDriverUnavailable, or ErrDriverNotExposed.
type RemoteDriverError struct {
	StatusCode       int
	Code             string
	Driver           DriverName
	AvailableDrivers []DriverName
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
		return ErrDriverNotInstalled
	case "driver_unavailable":
		return ErrDriverUnavailable
	case "driver_not_exposed":
		return ErrDriverNotExposed
	case "invalid_driver":
		return ErrInvalidArgument
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

func remoteStatusError(response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
	message := strings.TrimSpace(string(body))
	var payload struct {
		Error struct {
			Code             string       `json:"code"`
			Driver           DriverName   `json:"driver"`
			AvailableDrivers []DriverName `json:"available_drivers"`
			Message          string       `json:"message"`
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
