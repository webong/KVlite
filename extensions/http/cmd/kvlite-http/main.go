package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/webong/kvlite"
	kvlitehttp "github.com/webong/kvlite/extensions/http"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	flags := flag.NewFlagSet("kvlite-http", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	path := flags.String("path", "", "KVLite database directory to own")
	driver := flags.String("driver", "", "installed storage driver (defaults to Open default)")
	listen := flags.String("listen", "127.0.0.1:8089", "HTTP listen address")
	token := flags.String("token", "", "Bearer token required by clients")
	maxRequestBytes := flags.Int64("max-request-bytes", 64<<20, "maximum JSON request size")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *path == "" {
		fmt.Fprintln(os.Stderr, "kvlite-http: --path is required")
		return 2
	}

	dbOptions := make([]kvlite.Option, 0)
	if *driver != "" {
		dbOptions = append(dbOptions, kvlite.WithDriver(*driver))
	}
	db, err := kvlite.Open(*path, dbOptions...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kvlite-http: %v\n", err)
		return 1
	}
	defer db.Close()

	server, err := kvlitehttp.Serve(db, kvlitehttp.Options{
		ListenAddress:   *listen,
		BearerToken:     *token,
		MaxRequestBytes: *maxRequestBytes,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "kvlite-http: %v\n", err)
		return 1
	}
	defer server.Close()

	fmt.Printf("kvlite-http url=%s\n", server.URL())
	fmt.Println("kvlite-http: serving; press Ctrl-C to stop")

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	<-signals
	return 0
}
