package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/webong/kvlite"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		usage()
		return 0
	}
	if args[0] != "serve" {
		fmt.Fprintf(os.Stderr, "kvlite: unknown command %q\n", args[0])
		usage()
		return 2
	}
	serveFlags := flag.NewFlagSet("serve", flag.ContinueOnError)
	serveFlags.SetOutput(os.Stderr)
	path := serveFlags.String("path", "", "RocksDB directory to own")
	listen := serveFlags.String("listen", "127.0.0.1:0", "HTTP listen address")
	token := serveFlags.String("token", "", "Bearer token required by clients")
	maxRequestBytes := serveFlags.Int64("max-request-bytes", 64<<20, "maximum JSON request size")
	redisListen := serveFlags.String("redis-listen", "", "Redis RESP listen address (empty disables Redis)")
	redisPassword := serveFlags.String("redis-password", "", "password required by Redis AUTH")
	if err := serveFlags.Parse(args[1:]); err != nil {
		return 2
	}
	if *path == "" {
		fmt.Fprintln(os.Stderr, "kvlite: --path is required")
		return 2
	}
	options := []kvlite.Option{kvlite.WithSharing(kvlite.SharingOptions{
		ListenAddress:   *listen,
		BearerToken:     *token,
		MaxRequestBytes: *maxRequestBytes,
	})}
	if *redisListen != "" {
		options = append(options, kvlite.WithRedis(kvlite.RedisOptions{
			ListenAddress: *redisListen,
			Password:      *redisPassword,
		}))
	}
	db, err := kvlite.Open(*path, options...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kvlite: %v\n", err)
		return 1
	}
	defer db.Close()
	fmt.Printf("http=%s\n", db.SharingAddress())
	if address := db.RedisAddress(); address != "" {
		fmt.Printf("redis=%s\n", address)
	}
	fmt.Println("kvlite: serving; press Ctrl-C to stop")

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	<-signals
	return 0
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: kvlite serve --path DIR [--listen HOST:PORT] [--token TOKEN] [--redis-listen HOST:PORT] [--redis-password PASSWORD]")
	fmt.Fprintln(os.Stderr, "\nThe binary owns the RocksDB lock and exposes the language-neutral HTTP and optional Redis APIs.")
	fmt.Fprintln(os.Stderr, "Build with: go build -tags rocksdb ./cmd/kvlite")
}
