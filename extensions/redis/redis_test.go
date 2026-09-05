package kvliteredis

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/webong/kvlite"
)

type redisTestEngine struct {
	mu     sync.RWMutex
	values map[string][]byte
}

func newRedisTestEngine() *redisTestEngine {
	return &redisTestEngine{values: make(map[string][]byte)}
}

func (engine *redisTestEngine) Get(ctx context.Context, key []byte) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	value, found := engine.values[string(key)]
	return append([]byte(nil), value...), found, nil
}

func (engine *redisTestEngine) Put(ctx context.Context, key, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	engine.values[string(key)] = append([]byte(nil), value...)
	return nil
}

func (engine *redisTestEngine) Delete(ctx context.Context, key []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	delete(engine.values, string(key))
	return nil
}

func (engine *redisTestEngine) ScanPrefix(ctx context.Context, prefix []byte, callback func(key, value []byte) error) error {
	engine.mu.RLock()
	keys := make([]string, 0, len(engine.values))
	for key := range engine.values {
		if bytes.HasPrefix([]byte(key), prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	items := make([][2][]byte, 0, len(keys))
	for _, key := range keys {
		items = append(items, [2][]byte{[]byte(key), append([]byte(nil), engine.values[key]...)})
	}
	engine.mu.RUnlock()
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := callback(item[0], item[1]); err != nil {
			return err
		}
	}
	return nil
}

func (engine *redisTestEngine) Close() error { return nil }

func openRedisTestServer(t *testing.T, options Options) (*kvlite.DB, *Server) {
	t.Helper()
	db, err := kvlite.OpenWithEngine(newRedisTestEngine(), kvlite.BackendRocksDB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	server, err := Serve(db, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return db, server
}

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
	_, server := openRedisTestServer(t, Options{
		ListenAddress: "127.0.0.1:0",
		Password:      "secret",
	})
	client := newRedisTestClient(t, server.URL()[len("redis://"):])
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
	db, server := openRedisTestServer(t, Options{ListenAddress: "127.0.0.1:0"})
	client := newRedisTestClient(t, server.URL()[len("redis://"):])
	assertRedisSimple(t, client.do(t, "SET", "ephemeral", "value"), "OK")
	assertRedisInteger(t, client.do(t, "PEXPIRE", "ephemeral", "30"), 1)
	time.Sleep(60 * time.Millisecond)
	if reply := client.do(t, "GET", "ephemeral"); reply.kind != respBulk || !reply.null {
		t.Fatalf("expired GET = %#v", reply)
	}
	assertRedisInteger(t, client.do(t, "TTL", "ephemeral"), -2)
	assertRedisInteger(t, client.do(t, "SADD", "short", "member"), 1)
	assertRedisInteger(t, client.do(t, "PEXPIRE", "short", "30"), 1)
	time.Sleep(60 * time.Millisecond)
	if reply := client.do(t, "SCARD", "short"); reply.kind != respInteger || reply.value != 0 {
		t.Fatalf("expired set SCARD = %#v", reply)
	}
	if exists, err := db.Has(context.Background(), "ephemeral"); err != nil || exists {
		t.Fatalf("Has expired key = %t, %v", exists, err)
	}
}

func TestClosingServerLeavesEmbeddedOwnerOpen(t *testing.T) {
	db, server := openRedisTestServer(t, Options{ListenAddress: "127.0.0.1:0"})
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Put(context.Background(), "embedded", "still-open"); err != nil {
		t.Fatalf("owner Put() after Server.Close() = %v", err)
	}
}

func TestServeRemoteRejectsNilDatabase(t *testing.T) {
	if _, err := ServeRemote(nil, Options{ListenAddress: "127.0.0.1:0"}); err == nil {
		t.Fatal("ServeRemote(nil) unexpectedly succeeded")
	}
}

func TestServeRemoteServesRemoteHandle(t *testing.T) {
	db, err := kvlite.OpenWithEngine(newRedisTestEngine(), kvlite.BackendRemote)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if !db.IsRemote() {
		t.Fatal("OpenWithEngine handle is not marked remote")
	}
	server, err := ServeRemote(db, Options{ListenAddress: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	client := newRedisTestClient(t, server.URL()[len("redis://"):])
	assertRedisSimple(t, client.do(t, "PING"), "PONG")
	assertRedisSimple(t, client.do(t, "SET", "attached", "yes"), "OK")
	assertRedisBulk(t, client.do(t, "GET", "attached"), "yes")
	assertRedisInteger(t, client.do(t, "HSET", "profile", "name", "Ada"), 1)
	assertRedisBulk(t, client.do(t, "HGET", "profile", "name"), "Ada")
}
