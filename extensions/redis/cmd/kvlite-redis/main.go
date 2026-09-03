package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/webong/kvlite"
	kvliteredis "github.com/webong/kvlite/extensions/redis"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	flags := flag.NewFlagSet("kvlite-redis", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	path := flags.String("path", "", "KVLite database directory to own")
	driver := flags.String("driver", "", "installed storage driver (defaults to Open default)")
	listen := flags.String("listen", "127.0.0.1:6379", "Redis listen address")
	password := flags.String("password", "", "password required by AUTH")
	maxClients := flags.Int("max-clients", 0, "maximum simultaneous clients (0 for unlimited)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *path == "" {
		fmt.Fprintln(os.Stderr, "kvlite-redis: --path is required")
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

	server, err := kvliteredis.Serve(db, kvliteredis.Options{
		ListenAddress: *listen,
		Password:      *password,
		MaxClients:    *maxClients,
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
