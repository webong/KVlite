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
	"strconv"
	"strings"
	"time"
)

const defaultMaxRequestBytes int64 = 64 << 20

// SharingOptions configures the optional local HTTP transport.
type SharingOptions struct {
	ListenAddress   string
	BearerToken     string
	MaxRequestBytes int64
}

type shareServer struct {
	listener net.Listener
	server   *http.Server
}

type scanItem struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func startShareServer(db *DB, options SharingOptions) (*shareServer, error) {
	listener, err := net.Listen("tcp", options.ListenAddress)
	if err != nil {
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
	result := &shareServer{listener: listener, server: server}
	mux.HandleFunc("GET /v1", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Protocol string   `json:"protocol"`
			Entries  string   `json:"entries"`
			Methods  []string `json:"methods"`
		}{
			Protocol: "kvlite/1",
			Entries:  "/v1/entries/{base64url-key}",
			Methods:  []string{"GET", "PUT", "DELETE"},
		})
	})
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("/v1/entries/{key}", func(w http.ResponseWriter, request *http.Request) {
		handleJSONEntry(db, options.MaxRequestBytes, w, request)
	})
	mux.HandleFunc("/v1/kv/{key}", func(w http.ResponseWriter, request *http.Request) {
		key, err := base64.RawURLEncoding.DecodeString(request.PathValue("key"))
		if err != nil {
			http.Error(w, "invalid key", http.StatusBadRequest)
			return
		}
		switch request.Method {
		case http.MethodGet:
			value, found, err := db.engine.Get(request.Context(), key)
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
			if err := db.engine.Put(request.Context(), key, value); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodDelete:
			if err := db.engine.Delete(request.Context(), key); err != nil {
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
		prefix, err := base64.RawURLEncoding.DecodeString(request.URL.Query().Get("prefix"))
		if err != nil {
			http.Error(w, "invalid prefix", http.StatusBadRequest)
			return
		}
		items := make([]scanItem, 0)
		err = db.engine.ScanPrefix(request.Context(), prefix, func(key, value []byte) error {
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
		if err := json.NewEncoder(w).Encode(items); err != nil {
			return
		}
	})
	mux.HandleFunc("POST /v1/list/{key}/push", func(w http.ResponseWriter, request *http.Request) {
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
		db.collectionMu.Lock()
		defer db.collectionMu.Unlock()
		items, err := db.readList(request.Context(), key)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if request.URL.Query().Get("left") == "1" {
			items = append(newItems, items...)
		} else {
			items = append(items, newItems...)
		}
		if err := db.writeList(request.Context(), key, items); err != nil {
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
		return nil
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
	return newDB(&remoteEngine{baseURL: parsed.String(), token: remoteOptions.BearerToken, client: client}, cfg)
}

type remoteEngine struct {
	baseURL string
	token   string
	client  *http.Client
}

func (engine *remoteEngine) request(ctx context.Context, method, endpoint string, body io.Reader) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, engine.baseURL+endpoint, body)
	if err != nil {
		return nil, err
	}
	if engine.token != "" {
		request.Header.Set("Authorization", "Bearer "+engine.token)
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
	message, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
	return fmt.Errorf("kvlite: remote returned %s: %s", response.Status, strings.TrimSpace(string(message)))
}

func (engine *remoteEngine) Close() error {
	if transport, ok := engine.client.Transport.(interface{ CloseIdleConnections() }); ok {
		transport.CloseIdleConnections()
	}
	return nil
}
