//go:build !kvlite_no_linked_extensions

package main

import (
	"github.com/webong/kvlite"
	kvlitehttp "github.com/webong/kvlite/extensions/http"
	kvliteredis "github.com/webong/kvlite/extensions/redis"
)

func init() {
	isHTTPExtensionLinked = func() bool {
		_, err := kvlite.LinkedModule("http")
		return err == nil
	}
	isRedisExtensionLinked = func() bool {
		_, err := kvlite.LinkedModule("redis")
		return err == nil
	}
	linkedHTTPServe = func(database *kvlite.DB, options linkedHTTPServeConfig) (linkedHTTPServer, error) {
		return kvlitehttp.Serve(database, kvlitehttp.Options{
			ListenAddress:   options.listenAddress,
			BearerToken:     options.bearerToken,
			MaxRequestBytes: options.maxRequestBytes,
			DriverPaths:     options.driverPaths,
			RedisURL:        options.redisURL,
		})
	}
	linkedRedisServe = func(database *kvlite.DB, options linkedRedisServeConfig) (linkedRedisServer, error) {
		return kvliteredis.Serve(database, kvliteredis.Options{
			ListenAddress: options.listenAddress,
			Password:      options.password,
		})
	}
}
