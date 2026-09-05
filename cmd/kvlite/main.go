package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/webong/kvlite"
)

const (
	extensionModeAuto       = "auto"
	extensionModeLinked     = "linked"
	extensionModeStandalone = "standalone"
)

type linkedHTTPServer interface {
	URL() string
	Close() error
}

type linkedRedisServer interface {
	URL() string
	Close() error
}

type linkedHTTPServeConfig struct {
	listenAddress   string
	bearerToken     string
	maxRequestBytes int64
	driverPaths     map[kvlite.DriverName]string
	redisURL        string
}

type linkedRedisServeConfig struct {
	listenAddress string
	password      string
}

var isHTTPExtensionLinked = func() bool { return false }
var isRedisExtensionLinked = func() bool { return false }

var linkedHTTPServe = func(*kvlite.DB, linkedHTTPServeConfig) (linkedHTTPServer, error) {
	return nil, fmt.Errorf("http extension linking is not enabled in this binary")
}

var linkedRedisServe = func(*kvlite.DB, linkedRedisServeConfig) (linkedRedisServer, error) {
	return nil, fmt.Errorf("redis extension linking is not enabled in this binary")
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		usage()
		return 0
	}
	switch args[0] {
	case "driver":
		return runDriver(args[1:])
	case "module":
		return runModule(args[1:])
	case "serve":
	default:
		fmt.Fprintf(os.Stderr, "kvlite: unknown command %q\n", args[0])
		usage()
		return 2
	}
	serveFlags := flag.NewFlagSet("serve", flag.ContinueOnError)
	serveFlags.SetOutput(os.Stderr)
	listenExplicit := false
	for _, arg := range args[1:] {
		switch arg {
		case "--listen":
			listenExplicit = true
			continue
		}
		if strings.HasPrefix(arg, "--listen=") {
			listenExplicit = true
		}
	}
	path := serveFlags.String("path", "", "KVLite database directory to own")
	driver := serveFlags.String("driver", "", "installed storage driver (defaults to the bundle driver)")
	backend := serveFlags.String("backend", "", "deprecated alias for --driver")
	listen := serveFlags.String("listen", "127.0.0.1:0", "HTTP listen address")
	token := serveFlags.String("token", "", "Bearer token required by clients")
	maxRequestBytes := serveFlags.Int64("max-request-bytes", 64<<20, "maximum JSON request size")
	extensionMode := serveFlags.String("extension-mode", extensionModeAuto, "extension startup mode: auto|linked|standalone")
	redisListen := serveFlags.String("redis-listen", "", "Redis RESP listen address (empty disables Redis)")
	redisPassword := serveFlags.String("redis-password", "", "password required by Redis AUTH")
	var driverPaths driverPathValues
	serveFlags.Var(&driverPaths, "driver-path", "additional remote driver mapping in DRIVER=PATH form; repeatable")
	if err := serveFlags.Parse(args[1:]); err != nil {
		return 2
	}
	if *path == "" {
		fmt.Fprintln(os.Stderr, "kvlite: --path is required")
		return 2
	}
	if *backend != "" {
		if *driver != "" && *driver != *backend {
			fmt.Fprintln(os.Stderr, "kvlite: --driver and --backend select different drivers")
			return 2
		}
		*driver = *backend
	}
	httpListenExplicit := listenExplicit

	mode := strings.ToLower(strings.TrimSpace(*extensionMode))
	switch mode {
	case extensionModeAuto, extensionModeLinked, extensionModeStandalone:
	default:
		fmt.Fprintf(os.Stderr, "kvlite: unknown extension mode %q (want auto, linked, or standalone)\n", *extensionMode)
		return 2
	}

	if mode == extensionModeStandalone {
		if *redisListen != "" && httpListenExplicit {
			return runServeStandaloneBoth(
				*path,
				*driver,
				*listen,
				*token,
				*maxRequestBytes,
				*redisListen,
				*redisPassword,
			)
		}
		if *redisListen == "" {
			return runServeStandaloneHTTP(
				*path,
				*driver,
				*listen,
				*token,
				*maxRequestBytes,
				driverPaths.items,
			)
		}
		return runServeStandaloneRedis(*path, *driver, *redisListen, *redisPassword)
	}

	if mode == extensionModeAuto && !isHTTPExtensionLinked() {
		if len(driverPaths.items) > 0 {
			fmt.Fprintln(os.Stderr, "kvlite: --driver-path requires a linked HTTP extension")
			return 1
		}
		if *redisListen != "" && httpListenExplicit {
			return runServeStandaloneBoth(
				*path,
				*driver,
				*listen,
				*token,
				*maxRequestBytes,
				*redisListen,
				*redisPassword,
			)
		}
		if *redisListen != "" && !isRedisExtensionLinked() {
			return runServeStandaloneRedis(*path, *driver, *redisListen, *redisPassword)
		}
		if *redisListen == "" {
			return runServeStandaloneHTTP(*path, *driver, *listen, *token, *maxRequestBytes, driverPaths.items)
		}
	}

	if *driver == "" {
		*driver = string(kvlite.DefaultDriver())
	}

	db, err := kvlite.Open(*path, kvlite.WithDriver(*driver))
	if err != nil {
		fmt.Fprintf(os.Stderr, "kvlite: %v\n", err)
		return 1
	}
	defer db.Close()
	if *redisListen != "" && !isRedisExtensionLinked() {
		return runServeLinkedHTTPWithAttachedRedis(db, *listen, *token, *maxRequestBytes, driverPaths.items, *redisListen, *redisPassword)
	}
	var redisServer linkedRedisServer
	redisURL := ""
	if *redisListen != "" {
		redisServer, err = linkedRedisServe(db, linkedRedisServeConfig{
			listenAddress: *redisListen,
			password:      *redisPassword,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "kvlite: %v\n", err)
			return 1
		}
		redisURL = redisServer.URL()
		defer func() {
			if err := redisServer.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "kvlite: redis extension close failed: %v\n", err)
			}
		}()
	}

	server, err := linkedHTTPServe(db, linkedHTTPServeConfig{
		listenAddress:   *listen,
		bearerToken:     *token,
		maxRequestBytes: *maxRequestBytes,
		driverPaths:     driverPaths.items,
		redisURL:        redisURL,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "kvlite: %v\n", err)
		return 1
	}
	defer func() {
		if err := server.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "kvlite: http extension close failed: %v\n", err)
		}
	}()
	fmt.Printf("driver=%s\n", db.Backend())
	fmt.Printf("http=%s\n", server.URL())
	if redisServer != nil {
		fmt.Printf("redis=%s\n", redisServer.URL())
	}
	fmt.Println("kvlite: serving; press Ctrl-C to stop")

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	<-signals
	return 0
}

func runServeStandaloneHTTP(path, driver, listen, token string, maxRequestBytes int64, driverPaths map[kvlite.DriverName]string) int {
	if strings.TrimSpace(path) == "" {
		fmt.Fprintln(os.Stderr, "kvlite: --path is required")
		return 2
	}
	if strings.TrimSpace(listen) == "" {
		fmt.Fprintln(os.Stderr, "kvlite: --listen is required in standalone HTTP mode")
		return 2
	}
	if len(driverPaths) > 0 {
		fmt.Fprintln(os.Stderr, "kvlite: --driver-path is only supported in linked HTTP mode")
		return 2
	}
	args := []string{
		"http",
		"--path", path,
		"--listen", listen,
		"--max-request-bytes", strconv.FormatInt(maxRequestBytes, 10),
	}
	if driver != "" {
		args = append(args, "--driver", driver)
	}
	if token != "" {
		args = append(args, "--token", token)
	}
	fmt.Println("kvlite: starting standalone HTTP module")
	return runModuleRun(args)
}

func runServeStandaloneRedis(path, driver, listen, password string) int {
	if strings.TrimSpace(path) == "" {
		fmt.Fprintln(os.Stderr, "kvlite: --path is required")
		return 2
	}
	if strings.TrimSpace(listen) == "" {
		fmt.Fprintln(os.Stderr, "kvlite: --redis-listen is required for standalone Redis mode")
		return 2
	}
	args := []string{
		"redis",
		"--path", path,
		"--listen", listen,
	}
	if driver != "" {
		args = append(args, "--driver", driver)
	}
	if password != "" {
		args = append(args, "--password", password)
	}
	fmt.Println("kvlite: starting standalone Redis module")
	return runModuleRun(args)
}

// runServeStandaloneBoth serves HTTP and Redis from one database directory
// through two standalone processes in a shared-owner topology: a kvlite-http
// owner holds the single writable copy of the directory, and a kvlite-redis
// process attaches to it over the owner's loopback HTTP protocol. The owner
// must outlive the attached process; when the attached process exits, the
// owner is stopped and its exit status is returned.
func runServeStandaloneBoth(path, driver, listen, token string, maxRequestBytes int64, redisListen, redisPassword string) int {
	if strings.TrimSpace(path) == "" {
		fmt.Fprintln(os.Stderr, "kvlite: --path is required")
		return 2
	}
	if err := requireExplicitServePort(listen, "--listen"); err != nil {
		return 2
	}
	if err := requireExplicitServePort(redisListen, "--redis-listen"); err != nil {
		return 2
	}
	ownerArgs := []string{
		"--path", path,
		"--listen", listen,
		"--max-request-bytes", strconv.FormatInt(maxRequestBytes, 10),
	}
	if driver != "" {
		ownerArgs = append(ownerArgs, "--driver", driver)
	}
	if token != "" {
		ownerArgs = append(ownerArgs, "--token", token)
	}
	fmt.Println("kvlite: starting standalone HTTP owner module")
	owner, err := startModuleProcess("http", ownerArgs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kvlite: %v\n", err)
		return 1
	}
	ownerURL := "http://" + listen
	if !waitForOwnerHealth(ownerURL, token, owner) {
		// The health waiter already reaped the owner; Kill is a no-op if the
		// process is gone.
		_ = owner.Process.Kill()
		return 1
	}
	attachedArgs := []string{
		"--upstream", ownerURL,
		"--listen", redisListen,
	}
	if token != "" {
		attachedArgs = append(attachedArgs, "--upstream-token", token)
	}
	if driver != "" {
		attachedArgs = append(attachedArgs, "--upstream-driver", driver)
	}
	if redisPassword != "" {
		attachedArgs = append(attachedArgs, "--password", redisPassword)
	}
	fmt.Println("kvlite: starting standalone Redis module attached to the HTTP owner")
	attached, err := startModuleProcess("redis", attachedArgs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kvlite: %v\n", err)
		_ = owner.Process.Kill()
		return 1
	}
	status := waitForAttachedModule(owner, attached)
	return status
}

// runServeLinkedHTTPWithAttachedRedis serves the linked HTTP extension
// in-process and attaches a standalone Redis module to it. It covers the
// mixed build where HTTP is linked but Redis is only installed.
func runServeLinkedHTTPWithAttachedRedis(db *kvlite.DB, listen, token string, maxRequestBytes int64, driverPaths map[kvlite.DriverName]string, redisListen, redisPassword string) int {
	if err := requireExplicitServePort(redisListen, "--redis-listen"); err != nil {
		return 2
	}
	server, err := linkedHTTPServe(db, linkedHTTPServeConfig{
		listenAddress:   listen,
		bearerToken:     token,
		maxRequestBytes: maxRequestBytes,
		driverPaths:     driverPaths,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "kvlite: %v\n", err)
		return 1
	}
	defer func() {
		if err := server.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "kvlite: http extension close failed: %v\n", err)
		}
	}()
	attachedArgs := []string{
		"--upstream", server.URL(),
		"--listen", redisListen,
	}
	if token != "" {
		attachedArgs = append(attachedArgs, "--upstream-token", token)
	}
	if backend := db.Backend(); backend != "" && backend != kvlite.BackendRemote {
		attachedArgs = append(attachedArgs, "--upstream-driver", string(backend))
	}
	if redisPassword != "" {
		attachedArgs = append(attachedArgs, "--password", redisPassword)
	}
	fmt.Println("kvlite: starting standalone Redis module attached to the linked HTTP server")
	attached, err := startModuleProcess("redis", attachedArgs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kvlite: %v\n", err)
		return 1
	}
	fmt.Printf("driver=%s\n", db.Backend())
	fmt.Printf("http=%s\n", server.URL())
	fmt.Println("kvlite: serving; press Ctrl-C to stop")
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case sig := <-signals:
			_ = attached.Process.Signal(sig)
		case <-stop:
		}
	}()
	if err := attached.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if status := exitErr.ProcessState.ExitCode(); status > 0 {
				return status
			}
		}
		fmt.Fprintf(os.Stderr, "kvlite: attached redis module exited with error: %v\n", err)
		return 1
	}
	return 0
}

// requireExplicitServePort rejects ephemeral (:0) and unparsable addresses.
// An orchestrated owner/attached pair addresses each other by URL, which the
// parent cannot discover from child stdout, so both listeners need explicit
// ports. Single-protocol mode keeps its :0 default.
func requireExplicitServePort(address, flagName string) error {
	_, port, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil || strings.TrimSpace(port) == "" || port == "0" {
		fmt.Fprintf(os.Stderr, "kvlite: %s needs an explicit host:port when HTTP and Redis serve together (got %q)\n", flagName, address)
		if err != nil {
			return err
		}
		return fmt.Errorf("kvlite: %s needs an explicit host:port", flagName)
	}
	return nil
}

// waitForOwnerHealth polls the owner's health endpoint until it answers or
// the owner process exits. It reports false when the owner never becomes
// ready so the caller can stop waiting and surface the owner's own logs.
func waitForOwnerHealth(ownerURL, token string, owner *exec.Cmd) bool {
	exited := make(chan struct{})
	go func() {
		_, _ = owner.Process.Wait()
		close(exited)
	}()
	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-exited:
			fmt.Fprintln(os.Stderr, "kvlite: HTTP owner module exited before becoming ready")
			return false
		default:
		}
		request, err := http.NewRequest(http.MethodGet, ownerURL+"/v1/health", nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "kvlite: %v\n", err)
			return false
		}
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := client.Do(request)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return true
			}
		}
		select {
		case <-exited:
			fmt.Fprintln(os.Stderr, "kvlite: HTTP owner module exited before becoming ready")
			return false
		case <-time.After(200 * time.Millisecond):
		}
	}
	fmt.Fprintln(os.Stderr, "kvlite: HTTP owner module did not become ready in time")
	return false
}

// waitForAttachedModule waits for the attached Redis process while forwarding
// signals to both children. The owner is always stopped before returning.
func waitForAttachedModule(owner, attached *exec.Cmd) int {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case sig := <-signals:
			_ = attached.Process.Signal(sig)
			_ = owner.Process.Signal(sig)
		case <-stop:
		}
	}()
	waitErr := attached.Wait()
	// The health waiter reaps the owner in the background; Kill stops a live
	// owner and is a no-op otherwise.
	_ = owner.Process.Kill()
	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			if status := exitErr.ProcessState.ExitCode(); status > 0 {
				return status
			}
		}
		fmt.Fprintf(os.Stderr, "kvlite: attached redis module exited with error: %v\n", waitErr)
		return 1
	}
	return 0
}

type driverPathValues struct {
	items map[kvlite.DriverName]string
}

func (values *driverPathValues) String() string {
	if len(values.items) == 0 {
		return ""
	}
	pairs := make([]string, 0, len(values.items))
	for driver, path := range values.items {
		pairs = append(pairs, string(driver)+"="+path)
	}
	sort.Strings(pairs)
	return strings.Join(pairs, ",")
}

func (values *driverPathValues) Set(raw string) error {
	driver, path, found := strings.Cut(raw, "=")
	driver = strings.TrimSpace(driver)
	path = strings.TrimSpace(path)
	if !found || driver == "" || path == "" {
		return fmt.Errorf("driver mapping must use DRIVER=PATH")
	}
	if values.items == nil {
		values.items = make(map[kvlite.DriverName]string)
	}
	name := kvlite.DriverName(strings.ToLower(driver))
	if _, exists := values.items[name]; exists {
		return fmt.Errorf("driver mapping for %q was supplied more than once", name)
	}
	values.items[name] = path
	return nil
}

func runDriver(args []string) int {
	if len(args) != 1 || args[0] != "list" {
		fmt.Fprintln(os.Stderr, "Usage: kvlite driver list")
		return 2
	}
	for _, driver := range kvlite.Drivers() {
		fmt.Printf("%s\tavailable=%t\timplementation=%s\tformat=%s\tversion=%s\n",
			driver.Driver,
			driver.Available,
			driver.Implementation,
			driver.Format,
			driver.Version,
		)
	}
	return 0
}

func runModule(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: kvlite module list|run|verify [NAME]")
		return 2
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			fmt.Fprintln(os.Stderr, "Usage: kvlite module list")
			return 2
		}
		return runModuleList()
	case "run":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: kvlite module run <name> [args]")
			return 2
		}
		return runModuleRun(args[1:])
	case "exec":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: kvlite module exec <name> [args]")
			return 2
		}
		return runModuleRun(args[1:])
	case "verify":
		if len(args) > 2 {
			fmt.Fprintln(os.Stderr, "Usage: kvlite module verify [NAME]")
			return 2
		}
		name := ""
		if len(args) == 2 {
			name = args[1]
		}
		return runModuleVerify(name)
	default:
		fmt.Fprintf(os.Stderr, "kvlite: unknown module command %q\n", args[0])
		return 2
	}
}

func runModuleList() int {
	// Linked modules describe the code compiled into this binary. Installed
	// descriptors are deliberately listed separately: discovering a module must
	// never execute a library merely because it exists on disk.
	for _, module := range kvlite.LinkedModules() {
		printModule(module, "linked")
	}
	installed, err := kvlite.DiscoverModules()
	if err != nil {
		fmt.Fprintf(os.Stderr, "kvlite: %v\n", err)
		return 1
	}
	for _, module := range installed {
		printModule(module, "installed")
	}
	return 0
}

func runModuleVerify(name string) int {
	var (
		modules []kvlite.Module
		err     error
	)
	if name != "" {
		module, findErr := kvlite.FindInstalledModule(name)
		if findErr != nil {
			fmt.Fprintf(os.Stderr, "kvlite: %v\n", findErr)
			return 1
		}
		modules = []kvlite.Module{module}
	} else {
		modules, err = kvlite.DiscoverModules()
		if err != nil {
			fmt.Fprintf(os.Stderr, "kvlite: %v\n", err)
			return 1
		}
	}
	for _, module := range modules {
		if err := module.Verify(); err != nil {
			fmt.Fprintf(os.Stderr, "kvlite: module %s: %v\n", module.Manifest.Name, err)
			return 1
		}
		fmt.Printf("%s\tverified\n", module.Manifest.Name)
	}
	return 0
}

func printModule(module kvlite.Module, source string) {
	manifest := module.Manifest
	if module.Directory == "" {
		fmt.Printf("%s\tkind=%s\tversion=%s\tsource=%s\n", manifest.Name, manifest.Kind, manifest.Version, source)
		return
	}
	fmt.Printf("%s\tkind=%s\tversion=%s\tsource=%s\tpath=%s\n", manifest.Name, manifest.Kind, manifest.Version, source, module.Directory)
}

// startModuleProcess resolves, verifies, and starts an installed executable
// module without waiting for it. The caller owns the returned process.
func startModuleProcess(name string, subargs []string) (*exec.Cmd, error) {
	module, artifact, err := kvlite.ResolveModuleExecutable(name)
	if err != nil {
		return nil, err
	}
	artifactPath, err := module.ArtifactPath(artifact)
	if err != nil {
		return nil, fmt.Errorf("kvlite: module %s artifact path error: %w", name, err)
	}
	cmd := exec.Command(artifactPath, subargs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("kvlite: start module %q: %w", name, err)
	}
	return cmd, nil
}

func runModuleRun(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: kvlite module run <name> [args]")
		return 2
	}
	name := args[0]
	subargs := args[1:]
	cmd, err := startModuleProcess(name, subargs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kvlite: %v\n", err)
		return 1
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	stop := make(chan struct{})
	go func() {
		select {
		case sig := <-signals:
			_ = cmd.Process.Signal(sig)
		case <-stop:
		}
	}()
	waitErr := cmd.Wait()
	signal.Stop(signals)
	close(stop)

	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			if status := exitErr.ProcessState.ExitCode(); status > 0 {
				return status
			}
		}
		fmt.Fprintf(os.Stderr, "kvlite: module %q exited with error: %v\n", name, waitErr)
		return 1
	}
	return 0
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: kvlite serve --path DIR [--driver NAME] [--driver-path NAME=DIR] [--listen HOST:PORT] [--token TOKEN] [--redis-listen HOST:PORT] [--redis-password PASSWORD] [--extension-mode auto|linked|standalone]")
	fmt.Fprintln(os.Stderr, "       kvlite driver list")
	fmt.Fprintln(os.Stderr, "       kvlite module list|run <name> [args...]|verify [NAME]")
	fmt.Fprintln(os.Stderr, "\nDefaults prefer linked extensions, and fall back to standalone module binaries when auto-linked extensions are missing.")
	fmt.Fprintln(os.Stderr, "Standalone HTTP and Redis share one database through a shared-owner topology: a kvlite-http owner holds the directory and kvlite-redis attaches over the owner's loopback protocol. Serving both together needs explicit --listen and --redis-listen ports.")
	fmt.Fprintln(os.Stderr, "The binary can discover installed module descriptors from KVLITE_MODULE_PATH or KVLITE_HOME/{modules,drivers}; listing them never loads code.")
	fmt.Fprintln(os.Stderr, "Build a driver bundle with -tags kvlite_rocksdb,rocksdb; -tags kvlite_leveldb; or -tags kvlite_berkeleydb,berkeleydb, then inspect it with `kvlite driver list`.")
}
