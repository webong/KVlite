package kvlite

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"
)

type redisTestClient struct {
	conn   net.Conn
	reader *bufio.Reader
	writer *bufio.Writer
}

func newRedisTestClient(t *testing.T, address string) *redisTestClient {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	client := &redisTestClient{conn: conn, reader: bufio.NewReader(conn), writer: bufio.NewWriter(conn)}
	t.Cleanup(func() { _ = conn.Close() })
	return client
}

func (client *redisTestClient) do(t *testing.T, args ...string) respValue {
	t.Helper()
	items := make([]respValue, 0, len(args))
	for _, arg := range args {
		items = append(items, respBulkString(arg))
	}
	if err := writeRESP(client.writer, respArrayValues(items...)); err != nil {
		t.Fatal(err)
	}
	if err := client.writer.Flush(); err != nil {
		t.Fatal(err)
	}
	reply, err := readRESP(client.reader)
	if err != nil {
		t.Fatal(err)
	}
	return reply
}

func assertRedisSimple(t *testing.T, reply respValue, want string) {
	t.Helper()
	if reply.kind != respSimple || string(reply.data) != want {
		t.Fatalf("reply = %#v, want +%s", reply, want)
	}
}

func assertRedisBulk(t *testing.T, reply respValue, want string) {
	t.Helper()
	if reply.kind != respBulk || reply.null || !bytes.Equal(reply.data, []byte(want)) {
		t.Fatalf("reply = %#v, want $%q", reply, want)
	}
}

func assertRedisInteger(t *testing.T, reply respValue, want int64) {
	t.Helper()
	if reply.kind != respInteger || reply.value != want {
		t.Fatalf("reply = %#v, want :%d", reply, want)
	}
}

func TestRedisRESPCompatibility(t *testing.T) {
	db, _ := testDB(t, WithRedis(RedisOptions{
		ListenAddress: "127.0.0.1:0",
		Password:      "secret",
	}))
	client := newRedisTestClient(t, db.RedisAddress()[len("redis://"):])
	if reply := client.do(t, "PING"); reply.kind != respError || string(reply.data) != "NOAUTH Authentication required." {
		t.Fatalf("unauthenticated PING = %#v", reply)
	}
	if reply := client.do(t, "AUTH", "wrong"); reply.kind != respError {
		t.Fatalf("wrong AUTH = %#v", reply)
	}
	if reply := client.do(t, "HELLO", "3", "AUTH", "default", "wrong"); reply.kind != respError {
		t.Fatalf("wrong HELLO AUTH = %#v", reply)
	}
	if reply := client.do(t, "HELLO", "3", "AUTH", "default", "secret"); reply.kind != respMap || len(reply.items) == 0 {
		t.Fatalf("HELLO 3 = %#v", reply)
	}
	assertRedisSimple(t, client.do(t, "PING"), "PONG")

	assertRedisSimple(t, client.do(t, "SET", "foo", "bar", "EX", "60"), "OK")
	assertRedisBulk(t, client.do(t, "GET", "foo"), "bar")
	if reply := client.do(t, "TTL", "foo"); reply.kind != respInteger || reply.value < 1 || reply.value > 60 {
		t.Fatalf("TTL foo = %#v", reply)
	}
	assertRedisSimple(t, client.do(t, "SET", "ab", "sibling"), "OK")
	assertRedisInteger(t, client.do(t, "DEL", "foo"), 1)
	assertRedisBulk(t, client.do(t, "GET", "ab"), "sibling")

	assertRedisInteger(t, client.do(t, "HSET", "profile", "name", "Ada", "city", "Lagos"), 2)
	assertRedisBulk(t, client.do(t, "HGET", "profile", "name"), "Ada")
	if reply := client.do(t, "HGETALL", "profile"); reply.kind != respArray || len(reply.items) != 4 {
		t.Fatalf("HGETALL profile = %#v", reply)
	}
	assertRedisInteger(t, client.do(t, "SADD", "roles", "admin", "author", "admin"), 2)
	if reply := client.do(t, "SMEMBERS", "roles"); reply.kind != respArray || len(reply.items) != 2 {
		t.Fatalf("SMEMBERS roles = %#v", reply)
	}
	assertRedisInteger(t, client.do(t, "LPUSH", "jobs", "one", "two"), 2)
	if reply := client.do(t, "LRANGE", "jobs", "0", "-1"); reply.kind != respArray || len(reply.items) != 2 {
		t.Fatalf("LRANGE jobs = %#v", reply)
	} else {
		assertRedisBulk(t, reply.items[0], "two")
		assertRedisBulk(t, reply.items[1], "one")
	}
	if reply := client.do(t, "HGET", "ab", "field"); reply.kind != respError || string(reply.data) != errRedisWrongType.Error() {
		t.Fatalf("wrong type reply = %#v", reply)
	}
	assertRedisInteger(t, client.do(t, "INCRBY", "counter", "41"), 41)
	assertRedisInteger(t, client.do(t, "INCR", "counter"), 42)
	assertRedisBulk(t, client.do(t, "GET", "counter"), "42")
}

func TestRedisTTLAndCollectionsExpire(t *testing.T) {
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	db, _ := testDB(t,
		func(cfg *config) error {
			cfg.now = func() time.Time { return now }
			return nil
		},
		WithRedis(RedisOptions{ListenAddress: "127.0.0.1:0"}),
	)
	client := newRedisTestClient(t, db.RedisAddress()[len("redis://"):])
	assertRedisSimple(t, client.do(t, "SET", "ephemeral", "value"), "OK")
	assertRedisInteger(t, client.do(t, "EXPIRE", "ephemeral", "10"), 1)
	assertRedisInteger(t, client.do(t, "TTL", "ephemeral"), 10)
	now = now.Add(11 * time.Second)
	if reply := client.do(t, "GET", "ephemeral"); reply.kind != respBulk || !reply.null {
		t.Fatalf("expired GET = %#v", reply)
	}
	assertRedisInteger(t, client.do(t, "TTL", "ephemeral"), -2)
	assertRedisInteger(t, client.do(t, "SADD", "short", "member"), 1)
	assertRedisInteger(t, client.do(t, "EXPIRE", "short", "1"), 1)
	now = now.Add(2 * time.Second)
	if reply := client.do(t, "SCARD", "short"); reply.kind != respInteger || reply.value != 0 {
		t.Fatalf("expired set SCARD = %#v", reply)
	}
	if exists, err := db.Has(context.Background(), "ephemeral"); err != nil && !errors.Is(err, ErrNotFound) {
		t.Fatal(fmt.Errorf("Has expired key: %w", err))
	} else if exists {
		t.Fatal("expired key still exists")
	}
}
