package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/webong/kvlite"
	kvlitehttp "github.com/webong/kvlite/extensions/http"
	kvliteredis "github.com/webong/kvlite/extensions/redis"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	flags := flag.NewFlagSet("kvlite-redis", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	path := flags.String("path", "", "KVLite database directory to own (direct-owner mode)")
	driver := flags.String("driver", "", "installed storage driver (defaults to Open default)")
	upstream := flags.String("upstream", "", "HTTP owner base URL to attach to (shared-owner mode, e.g. http://127.0.0.1:8089)")
	upstreamToken := flags.String("upstream-token", "", "bearer token for the HTTP owner")
	upstreamDriver := flags.String("upstream-driver", "", "remote driver selected on the HTTP owner")
	listen := flags.String("listen", "127.0.0.1:6379", "Redis listen address")
	password := flags.String("password", "", "password required by AUTH")
	maxClients := flags.Int("max-clients", 0, "maximum simultaneous clients (0 for unlimited)")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	if *upstream != "" {
		return runAttached(*upstream, *upstreamToken, *upstreamDriver, *listen, *password, *maxClients, *path, *driver)
	}
	if *path == "" {
		fmt.Fprintln(os.Stderr, "kvlite-redis: --path is required in direct-owner mode (or use --upstream for shared-owner mode)")
		return 2
	}

	dbOptions := make([]kvlite.Option, 0)
	if *driver != "" {
		dbOptions = append(dbOptions, kvlite.WithDriver(*driver))
	}
	db, err := kvlite.Open(*path, dbOptions...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kvlite-redis: %v\n", err)
		return 1
	}
	defer db.Close()

	return serve(db, *listen, *password, *maxClients)
}

// runAttached serves Redis from a remote database owned by an HTTP owner
// process. The owner holds the single writable copy of the driver directory;
// this process never opens it. Per-command failures (owner down, token
// rejected) are returned to Redis clients as ERR replies; the server keeps
// serving until it is stopped.
func runAttached(upstream, token, driver, listen, password string, maxClients int, path, localDriver string) int {
	if path != "" || localDriver != "" {
		fmt.Fprintln(os.Stderr, "kvlite-redis: --upstream cannot be combined with --path or --driver")
		return 2
	}
	if strings.TrimSpace(listen) == "" {
		fmt.Fprintln(os.Stderr, "kvlite-redis: --listen is required")
		return 2
	}
	baseURL := strings.TrimRight(strings.TrimSpace(upstream), "/")
	if baseURL == "" {
		fmt.Fprintln(os.Stderr, "kvlite-redis: --upstream must be an http(s) URL")
		return 2
	}
	clientOptions := kvlitehttp.ClientOptions{BearerToken: token}
	if driver != "" {
		name, err := kvlite.ParseDriverName(driver)
		if err != nil {
			fmt.Fprintf(os.Stderr, "kvlite-redis: %v\n", err)
			return 2
		}
		clientOptions.Driver = name
	}
	if err := checkOwnerHealth(baseURL, token); err != nil {
		fmt.Fprintf(os.Stderr, "kvlite-redis: upstream owner %s is not reachable: %v\n", baseURL, err)
		return 1
	}
	remote, err := kvlitehttp.Connect(baseURL, clientOptions)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kvlite-redis: connect to upstream owner: %v\n", err)
		return 1
	}
	defer remote.Close()

	server, err := kvliteredis.ServeRemote(remote, kvliteredis.Options{
		ListenAddress: listen,
		Password:      password,
		MaxClients:    maxClients,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "kvlite-redis: %v\n", err)
		return 1
	}
	defer server.Close()

	fmt.Printf("kvlite-redis url=%s upstream=%s\n", server.URL(), baseURL)
	fmt.Println("kvlite-redis: serving attached; press Ctrl-C to stop")

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	<-signals
	return 0
}

// checkOwnerHealth validates that an HTTP owner is reachable and accepts the
// bearer token before this process commits to serving from it. Without an
// explicit driver selection Connect performs no request, so every attached
// startup checks /v1/health explicitly.
func checkOwnerHealth(baseURL, token string) error {
	request, err := http.NewRequest(http.MethodGet, baseURL+"/v1/health", nil)
	if err != nil {
		return err
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("owner returned status %s (check the URL and token)", response.Status)
	}
	var health struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(response.Body).Decode(&health); err != nil {
		return fmt.Errorf("owner returned an unreadable health reply: %w", err)
	}
	if health.Status != "ok" {
		return fmt.Errorf("owner reported status %q", health.Status)
	}
	return nil
}

func serve(db *kvlite.DB, listen, password string, maxClients int) int {
	server, err := kvliteredis.Serve(db, kvliteredis.Options{
		ListenAddress: listen,
		Password:      password,
		MaxClients:    maxClients,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "kvlite-redis: %v\n", err)
		return 1
	}
	defer server.Close()

	fmt.Printf("kvlite-redis url=%s\n", server.URL())
	fmt.Println("kvlite-redis: serving; press Ctrl-C to stop")

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	<-signals
	return 0
}
