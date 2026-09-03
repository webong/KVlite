package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"

	"github.com/webong/kvlite"
	kvlitehttp "github.com/webong/kvlite/extensions/http"
	kvliteredis "github.com/webong/kvlite/extensions/redis"
)

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
	path := serveFlags.String("path", "", "KVLite database directory to own")
	driver := serveFlags.String("driver", "", "installed storage driver (defaults to the bundle driver)")
	backend := serveFlags.String("backend", "", "deprecated alias for --driver")
	listen := serveFlags.String("listen", "127.0.0.1:0", "HTTP listen address")
	token := serveFlags.String("token", "", "Bearer token required by clients")
	maxRequestBytes := serveFlags.Int64("max-request-bytes", 64<<20, "maximum JSON request size")
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
	if *driver == "" {
		*driver = string(kvlite.DefaultDriver())
	}
	db, err := kvlite.Open(*path, kvlite.WithDriver(*driver))
	if err != nil {
		fmt.Fprintf(os.Stderr, "kvlite: %v\n", err)
		return 1
	}
	defer db.Close()
	var redisServer *kvliteredis.Server
	redisURL := ""
	if *redisListen != "" {
		redisServer, err = kvliteredis.Serve(db, kvliteredis.Options{
			ListenAddress: *redisListen,
			Password:      *redisPassword,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "kvlite: %v\n", err)
			return 1
		}
		defer redisServer.Close()
		redisURL = redisServer.URL()
	}
	server, err := kvlitehttp.Serve(db, kvlitehttp.Options{
		ListenAddress:   *listen,
		BearerToken:     *token,
		MaxRequestBytes: *maxRequestBytes,
		DriverPaths:     driverPaths.items,
		RedisURL:        redisURL,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "kvlite: %v\n", err)
		return 1
	}
	defer server.Close()
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
		fmt.Fprintln(os.Stderr, "Usage: kvlite module list|verify [NAME]")
		return 2
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			fmt.Fprintln(os.Stderr, "Usage: kvlite module list")
			return 2
		}
		return runModuleList()
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

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: kvlite serve --path DIR [--driver NAME] [--driver-path NAME=DIR] [--listen HOST:PORT] [--token TOKEN] [--redis-listen HOST:PORT] [--redis-password PASSWORD]")
	fmt.Fprintln(os.Stderr, "       kvlite driver list")
	fmt.Fprintln(os.Stderr, "       kvlite module list|verify [NAME]")
	fmt.Fprintln(os.Stderr, "\nThe binary links KVLite's optional HTTP and Redis extensions and owns server-defined driver/path mappings.")
	fmt.Fprintln(os.Stderr, "Installed module descriptors are discovered only from KVLITE_MODULE_PATH or KVLITE_HOME/{modules,drivers}; listing them never loads code.")
	fmt.Fprintln(os.Stderr, "Build a driver bundle with -tags kvlite_rocksdb,rocksdb; -tags kvlite_leveldb; or -tags kvlite_berkeleydb,berkeleydb, then inspect it with `kvlite driver list`.")
}
