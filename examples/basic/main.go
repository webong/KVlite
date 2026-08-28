//go:build rocksdb

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/webong/kvlite"
)

type user struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func main() {
	db, err := kvlite.Open("./kvlite-data")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	want := user{ID: 101, Name: "Ada"}
	if err := db.Put(ctx, "user:101", want, kvlite.TTL(time.Hour)); err != nil {
		log.Fatal(err)
	}

	got, err := kvlite.GetAs[user](ctx, db, "user:101")
	if errors.Is(err, kvlite.ErrNotFound) {
		log.Fatal("user expired")
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("loaded user %d: %s\n", got.ID, got.Name)
}
