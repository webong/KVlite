// Package kvliteredis adds KVLite's optional Redis-compatible RESP server.
// Importing the embeddable kvlite core alone never starts a listener.
package kvliteredis

import (
	"bufio"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/webong/kvlite"
)

func init() {
	kvlite.MustRegisterLinkedModule(Manifest())
}

// Manifest describes the optional Redis-compatible transport. It is linked
// only by callers that import this package and starts a listener only through
// its explicit Serve API.
func Manifest() kvlite.ModuleManifest {
	return kvlite.ModuleManifest{
		SchemaVersion: kvlite.ModuleManifestVersion,
		Name:          "redis",
		Kind:          kvlite.ModuleKindExtension,
		Version:       "v0.1.0",
		ModuleABI:     kvlite.ModuleABIVersion,
		Capabilities:  []string{"redis-resp2", "redis-server"},
		License:       "Apache-2.0",
	}
}

// Options configures the optional Redis protocol endpoint. The endpoint
// speaks RESP2 and runs in the same process as the owning DB handle.
//
// A password enables AUTH. MaxClients is zero for no explicit connection
// limit; a positive value bounds active client connections.
type Options struct {
	ListenAddress string
	Password      string
	MaxClients    int
}

// Server owns a Redis-compatible listener. Closing it never closes the
// caller-owned embedded KVLite DB.
type Server struct {
	listener net.Listener
	done     chan struct{}
	once     sync.Once
	wg       sync.WaitGroup

	mu     sync.Mutex
	closed bool
	conns  map[net.Conn]struct{}
	sem    chan struct{}
	id     atomic.Uint64
}

type redisSession struct {
	authed bool
	id     uint64
	proto  int
}

// Serve starts an explicit Redis-compatible RESP server for db.
func Serve(db *kvlite.DB, options Options) (*Server, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: database is required", kvlite.ErrInvalidArgument)
	}
	if db.Backend() == kvlite.BackendRemote {
		return nil, fmt.Errorf("%w: a remote database cannot own a Redis server", kvlite.ErrInvalidArgument)
	}
	if options.ListenAddress == "" {
		options.ListenAddress = "127.0.0.1:6379"
	}
	if options.MaxClients < 0 {
		return nil, fmt.Errorf("%w: max clients cannot be negative", kvlite.ErrInvalidArgument)
	}
	listener, err := net.Listen("tcp", options.ListenAddress)
	if err != nil {
		return nil, fmt.Errorf("kvlite: start Redis endpoint: %w", err)
	}
	server := &Server{
		listener: listener,
		done:     make(chan struct{}),
		conns:    make(map[net.Conn]struct{}),
	}
	if options.MaxClients > 0 {
		server.sem = make(chan struct{}, options.MaxClients)
	}
	server.wg.Add(1)
	go server.acceptLoop(newDatabase(db.Protocol()), options)
	return server, nil
}

func (server *Server) acceptLoop(db *database, options Options) {
	defer server.wg.Done()
	for {
		conn, err := server.listener.Accept()
		if err != nil {
			select {
			case <-server.done:
				return
			default:
			}
			if temporary, ok := err.(net.Error); ok && temporary.Temporary() {
				time.Sleep(25 * time.Millisecond)
				continue
			}
			return
		}
		if server.sem != nil {
			select {
			case server.sem <- struct{}{}:
			default:
				_, _ = io.WriteString(conn, "-ERR max number of clients reached\r\n")
				_ = conn.Close()
				continue
			}
		}
		if !server.register(conn) {
			if server.sem != nil {
				<-server.sem
			}
			_ = conn.Close()
			return
		}
		session := redisSession{authed: options.Password == "", id: server.id.Add(1), proto: 2}
		server.wg.Add(1)
		go func() {
			defer server.wg.Done()
			defer server.unregister(conn)
			defer conn.Close()
			if server.sem != nil {
				defer func() { <-server.sem }()
			}
			serveRedisConnection(db, conn, options.Password, &session)
		}()
	}
}

func (server *Server) register(conn net.Conn) bool {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.closed {
		return false
	}
	server.conns[conn] = struct{}{}
	return true
}

func (server *Server) unregister(conn net.Conn) {
	server.mu.Lock()
	delete(server.conns, conn)
	server.mu.Unlock()
}

// URL reports the listener address in Redis URL form.
func (server *Server) URL() string {
	return "redis://" + server.listener.Addr().String()
}

// Close stops the Redis listener and active clients. It is safe to call more
// than once and does not close the caller-owned DB.
func (server *Server) Close() error {
	var closeErr error
	server.once.Do(func() {
		close(server.done)
		server.mu.Lock()
		server.closed = true
		connections := make([]net.Conn, 0, len(server.conns))
		for conn := range server.conns {
			connections = append(connections, conn)
		}
		server.mu.Unlock()
		if err := server.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			closeErr = err
		}
		for _, conn := range connections {
			_ = conn.Close()
		}
		server.wg.Wait()
	})
	return closeErr
}

func serveRedisConnection(db *database, conn net.Conn, password string, session *redisSession) {
	reader := bufio.NewReaderSize(conn, 64<<10)
	writer := bufio.NewWriterSize(conn, 64<<10)
	for {
		request, err := readRESP(reader)
		if err != nil {
			return
		}
		args, err := redisCommandArgs(request)
		if err != nil {
			if writeRedisReply(writer, respErrorString("ERR "+err.Error())) != nil {
				return
			}
			continue
		}
		db.store.Lock()
		reply, quit := db.redisDispatch(args, password, session)
		db.store.Unlock()
		if err := writeRedisReply(writer, reply); err != nil {
			return
		}
		if quit {
			return
		}
	}
}

func writeRedisReply(writer *bufio.Writer, reply respValue) error {
	if err := writeRESP(writer, reply); err != nil {
		return err
	}
	return writer.Flush()
}

func redisCommandArgs(request respValue) ([][]byte, error) {
	if request.kind != respArray || request.null || len(request.items) == 0 {
		return nil, errors.New("expected a non-empty command array")
	}
	args := make([][]byte, len(request.items))
	for i, item := range request.items {
		if item.null {
			return nil, errors.New("null command argument")
		}
		switch item.kind {
		case respBulk, respSimple:
			args[i] = append([]byte(nil), item.data...)
		case respInteger:
			args[i] = []byte(strconv.FormatInt(item.value, 10))
		default:
			return nil, errors.New("command arguments must be strings")
		}
	}
	return args, nil
}

func (db *database) redisDispatch(args [][]byte, password string, session *redisSession) (respValue, bool) {
	command := strings.ToUpper(string(args[0]))

	// HELLO can authenticate inline (HELLO 3 AUTH default password), which is
	// how newer Redis clients negotiate protocol and credentials in one call.
	if command == "HELLO" {
		if reply, ok := db.redisHello(args, password, session); ok {
			return reply, false
		}
	}
	if command == "AUTH" {
		return db.redisAuth(args, password, session), false
	}
	if command == "QUIT" {
		return respSimpleString("OK"), true
	}
	if password != "" && !session.authed {
		return respErrorString("NOAUTH Authentication required."), false
	}

	switch command {
	case "PING":
		if len(args) == 1 {
			return respSimpleString("PONG"), false
		}
		if len(args) == 2 {
			return respBulkBytes(args[1]), false
		}
		return redisSyntaxError(), false
	case "ECHO":
		if len(args) != 2 {
			return redisSyntaxError(), false
		}
		return respBulkBytes(args[1]), false
	case "HELLO":
		// redisHello already handled successful negotiation. A malformed HELLO
		// reaches this branch so it gets a normal syntax error.
		return redisSyntaxError(), false
	case "SELECT":
		if len(args) != 2 || string(args[1]) != "0" {
			return respErrorString("ERR DB index is out of range"), false
		}
		return respSimpleString("OK"), false
	case "CLIENT":
		return db.redisClient(args, session), false
	case "COMMAND":
		return db.redisCommandInfo(args), false
	case "ROLE":
		return respArrayValues(respBulkString("master"), respArrayValues()), false
	case "READONLY", "READWRITE":
		return respSimpleString("OK"), false
	case "GET":
		return db.redisGet(args), false
	case "SET":
		return db.redisSet(args), false
	case "SETNX":
		return db.redisSetNX(args), false
	case "SETEX":
		return db.redisSetEX(args, false), false
	case "PSETEX":
		return db.redisSetEX(args, true), false
	case "GETSET":
		return db.redisGetSet(args), false
	case "GETDEL":
		return db.redisGetDel(args), false
	case "GETEX":
		return db.redisGetEx(args), false
	case "MGET":
		return db.redisMGet(args), false
	case "MSET":
		return db.redisMSet(args), false
	case "MSETNX":
		return db.redisMSetNX(args), false
	case "STRLEN":
		return db.redisStrLen(args), false
	case "APPEND":
		return db.redisAppend(args), false
	case "INCR":
		return db.redisIncrementCommand(args, 1), false
	case "INCRBY":
		return db.redisIncrementByCommand(args, false), false
	case "DECR":
		return db.redisIncrementCommand(args, -1), false
	case "DECRBY":
		return db.redisIncrementByCommand(args, true), false
	case "DEL", "UNLINK":
		return db.redisDelete(args), false
	case "EXISTS":
		return db.redisExists(args), false
	case "TYPE":
		return db.redisTypeCommand(args), false
	case "EXPIRE", "PEXPIRE", "EXPIREAT", "PEXPIREAT":
		return db.redisExpireCommand(args, command), false
	case "TTL", "PTTL":
		return db.redisTTLCommand(args, command), false
	case "PERSIST":
		return db.redisPersistCommand(args), false
	case "HSET", "HMSET":
		return db.redisHSet(args), false
	case "HGET":
		return db.redisHGet(args), false
	case "HMGET":
		return db.redisHMGet(args), false
	case "HGETALL":
		return db.redisHGetAll(args), false
	case "HDEL":
		return db.redisHDel(args), false
	case "HEXISTS":
		return db.redisHExists(args), false
	case "HLEN":
		return db.redisHLen(args), false
	case "HKEYS":
		return db.redisHKeys(args), false
	case "HVALS":
		return db.redisHVals(args), false
	case "HINCRBY":
		return db.redisHIncrBy(args), false
	case "SADD":
		return db.redisSAdd(args), false
	case "SREM":
		return db.redisSRem(args), false
	case "SISMEMBER":
		return db.redisSIsMember(args), false
	case "SMISMEMBER":
		return db.redisSMIsMember(args), false
	case "SMEMBERS":
		return db.redisSMembers(args), false
	case "SCARD":
		return db.redisSCard(args), false
	case "LPUSH", "RPUSH":
		return db.redisPush(args, command == "LPUSH"), false
	case "LPUSHX", "RPUSHX":
		return db.redisPushX(args, command == "LPUSHX"), false
	case "LRANGE":
		return db.redisLRange(args), false
	case "LLEN":
		return db.redisLLen(args), false
	case "LPOP", "RPOP":
		return db.redisPop(args, command == "LPOP"), false
	case "LTRIM":
		return db.redisLTrim(args), false
	case "SCAN":
		return db.redisScan(args), false
	case "KEYS":
		return db.redisKeysCommand(args), false
	case "DBSIZE":
		return db.redisDBSize(args), false
	case "FLUSHDB", "FLUSHALL":
		return db.redisFlush(args), false
	case "CONFIG":
		return db.redisConfig(args), false
	case "INFO":
		return db.redisInfo(args), false
	case "TIME":
		now := db.cfg.now()
		return respArrayValues(respBulkString(strconv.FormatInt(now.Unix(), 10)), respBulkString(strconv.FormatInt(int64(now.Nanosecond()/1000), 10))), false
	default:
		return respErrorString("ERR unknown command '" + command + "'"), false
	}
}

func redisSyntaxError() respValue { return respErrorString("ERR syntax error") }

func (db *database) redisAuth(args [][]byte, password string, session *redisSession) respValue {
	if password == "" {
		return respErrorString("ERR AUTH called without any password configured")
	}
	var supplied []byte
	switch len(args) {
	case 2:
		supplied = args[1]
	case 3:
		// ACL usernames are intentionally not modelled; the single configured
		// password still works with clients that send AUTH default password.
		supplied = args[2]
	default:
		return redisSyntaxError()
	}
	if subtle.ConstantTimeCompare(supplied, []byte(password)) != 1 {
		return respErrorString("WRONGPASS invalid username-password pair or user is disabled.")
	}
	session.authed = true
	return respSimpleString("OK")
}

func (db *database) redisHello(args [][]byte, password string, session *redisSession) (respValue, bool) {
	if len(args) < 1 {
		return redisSyntaxError(), true
	}
	version := "2"
	if len(args) >= 2 {
		version = string(args[1])
	}
	if version != "2" && version != "3" {
		return respErrorString("NOPROTO unsupported RESP version"), true
	}
	for i := 2; i < len(args); {
		switch strings.ToUpper(string(args[i])) {
		case "AUTH":
			if i+2 >= len(args) {
				return redisSyntaxError(), true
			}
			if password == "" || subtle.ConstantTimeCompare(args[i+2], []byte(password)) != 1 {
				return respErrorString("WRONGPASS invalid username-password pair or user is disabled."), true
			}
			session.authed = true
			i += 3
		case "SETNAME":
			if i+1 >= len(args) {
				return redisSyntaxError(), true
			}
			i += 2
		default:
			return respErrorString("ERR syntax error"), true
		}
	}
	if password != "" && !session.authed {
		return respErrorString("NOAUTH Authentication required."), true
	}
	session.proto = 2
	replyItems := []respValue{
		respBulkString("server"), respBulkString("kvlite"),
		respBulkString("version"), respBulkString("0.1.0"),
		respBulkString("proto"), respIntegerValue(2),
		respBulkString("id"), respIntegerValue(int64(session.id)),
		respBulkString("mode"), respBulkString("standalone"),
		respBulkString("role"), respBulkString("master"),
		respBulkString("modules"), respArrayValues(),
	}
	if version == "3" {
		session.proto = 3
		return respMapValues(replyItems...), true
	}
	return respArrayValues(replyItems...), true
}

func (db *database) redisClient(args [][]byte, session *redisSession) respValue {
	if len(args) < 2 {
		return redisSyntaxError()
	}
	switch strings.ToUpper(string(args[1])) {
	case "ID":
		if len(args) != 2 {
			return redisSyntaxError()
		}
		return respIntegerValue(int64(session.id))
	case "SETNAME", "SETINFO", "NO-EVICT", "REPLY", "PAUSE", "UNPAUSE", "TRACKING", "CACHING":
		return respSimpleString("OK")
	default:
		return respErrorString("ERR unknown subcommand '" + strings.ToUpper(string(args[1])) + "'")
	}
}

func (db *database) redisCommandInfo(args [][]byte) respValue {
	if len(args) == 1 || (len(args) == 2 && strings.EqualFold(string(args[1]), "INFO")) || (len(args) == 2 && strings.EqualFold(string(args[1]), "COUNT")) {
		return respArrayValues()
	}
	return respArrayValues()
}

func (db *database) redisConfig(args [][]byte) respValue {
	if len(args) < 2 {
		return redisSyntaxError()
	}
	if strings.EqualFold(string(args[1]), "GET") {
		return respArrayValues()
	}
	return respSimpleString("OK")
}

func (db *database) redisInfo(args [][]byte) respValue {
	if len(args) > 2 {
		return redisSyntaxError()
	}
	return respBulkString("# Server\r\nredis_version:7.2.0\r\nredis_mode:standalone\r\n\r\n# KVLite\r\nkvlite_version:0.1.0\r\n")
}
