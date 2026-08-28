package kvlite

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	respSimple  byte = '+'
	respError   byte = '-'
	respInteger byte = ':'
	respBulk    byte = '$'
	respArray   byte = '*'
	respMap     byte = '%'
)

const (
	maxRESPLine = 1 << 20
	maxRESPBulk = 64 << 20
	maxRESPArgs = 1024
)

type respValue struct {
	kind  byte
	data  []byte
	value int64
	items []respValue
	null  bool
}

func readRESP(reader *bufio.Reader) (respValue, error) {
	prefix, err := reader.ReadByte()
	if err != nil {
		return respValue{}, err
	}
	switch prefix {
	case respArray:
		count, err := readRESPInt(reader)
		if err != nil {
			return respValue{}, err
		}
		if count < 0 || count > maxRESPArgs {
			return respValue{}, fmt.Errorf("redis: invalid array length %d", count)
		}
		items := make([]respValue, count)
		for i := range items {
			item, err := readRESP(reader)
			if err != nil {
				return respValue{}, err
			}
			if item.null {
				return respValue{}, fmt.Errorf("redis: null command argument")
			}
			items[i] = item
		}
		return respValue{kind: respArray, items: items}, nil
	case respMap:
		count, err := readRESPInt(reader)
		if err != nil {
			return respValue{}, err
		}
		if count < 0 || count > maxRESPArgs {
			return respValue{}, fmt.Errorf("redis: invalid map length %d", count)
		}
		items := make([]respValue, 0, count*2)
		for i := 0; i < count*2; i++ {
			item, err := readRESP(reader)
			if err != nil {
				return respValue{}, err
			}
			if item.null {
				return respValue{}, fmt.Errorf("redis: null map member")
			}
			items = append(items, item)
		}
		return respValue{kind: respMap, items: items}, nil
	case respBulk:
		length, err := readRESPInt(reader)
		if err != nil {
			return respValue{}, err
		}
		if length == -1 {
			return respValue{kind: respBulk, null: true}, nil
		}
		if length < 0 || length > maxRESPBulk {
			return respValue{}, fmt.Errorf("redis: invalid bulk length %d", length)
		}
		data := make([]byte, length)
		if _, err := io.ReadFull(reader, data); err != nil {
			return respValue{}, err
		}
		if err := expectCRLF(reader); err != nil {
			return respValue{}, err
		}
		return respValue{kind: respBulk, data: data}, nil
	case respSimple, respError, respInteger:
		line, err := readRESPLine(reader)
		if err != nil {
			return respValue{}, err
		}
		if prefix == respInteger {
			value, err := strconv.ParseInt(string(line), 10, 64)
			if err != nil {
				return respValue{}, fmt.Errorf("redis: invalid integer: %w", err)
			}
			return respValue{kind: respInteger, value: value}, nil
		}
		return respValue{kind: prefix, data: line}, nil
	default:
		// Redis also accepts inline commands. The first byte already read is
		// part of the command line, which is useful for telnet and simple CLIs.
		line, err := readRESPLineAfterPrefix(reader, prefix)
		if err != nil {
			return respValue{}, err
		}
		parts := strings.Fields(string(line))
		if len(parts) == 0 || len(parts) > maxRESPArgs {
			return respValue{}, fmt.Errorf("redis: invalid inline command")
		}
		items := make([]respValue, len(parts))
		for i, part := range parts {
			items[i] = respValue{kind: respBulk, data: []byte(part)}
		}
		return respValue{kind: respArray, items: items}, nil
	}
}

func readRESPInt(reader *bufio.Reader) (int, error) {
	line, err := readRESPLine(reader)
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseInt(string(line), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("redis: invalid length: %w", err)
	}
	return int(value), nil
}

func readRESPLine(reader *bufio.Reader) ([]byte, error) {
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	if len(line) < 2 || line[len(line)-2] != '\r' {
		return nil, fmt.Errorf("redis: malformed CRLF")
	}
	line = line[:len(line)-2]
	if len(line) > maxRESPLine {
		return nil, fmt.Errorf("redis: line too long")
	}
	return line, nil
}

func readRESPLineAfterPrefix(reader *bufio.Reader, prefix byte) ([]byte, error) {
	line, err := readRESPLine(reader)
	if err != nil {
		return nil, err
	}
	return append([]byte{prefix}, line...), nil
}

func expectCRLF(reader *bufio.Reader) error {
	var suffix [2]byte
	if _, err := io.ReadFull(reader, suffix[:]); err != nil {
		return err
	}
	if !bytes.Equal(suffix[:], []byte("\r\n")) {
		return fmt.Errorf("redis: malformed bulk terminator")
	}
	return nil
}

func writeRESP(writer *bufio.Writer, reply respValue) error {
	switch reply.kind {
	case respSimple, respError:
		if bytes.Contains(reply.data, []byte("\r")) || bytes.Contains(reply.data, []byte("\n")) {
			reply.data = bytes.ReplaceAll(bytes.ReplaceAll(reply.data, []byte("\r"), nil), []byte("\n"), nil)
		}
		if err := writer.WriteByte(reply.kind); err != nil {
			return err
		}
		if _, err := writer.Write(reply.data); err != nil {
			return err
		}
		_, err := writer.WriteString("\r\n")
		return err
	case respInteger:
		_, err := fmt.Fprintf(writer, ":%d\r\n", reply.value)
		return err
	case respBulk:
		if reply.null {
			_, err := writer.WriteString("$-1\r\n")
			return err
		}
		if _, err := fmt.Fprintf(writer, "$%d\r\n", len(reply.data)); err != nil {
			return err
		}
		if _, err := writer.Write(reply.data); err != nil {
			return err
		}
		_, err := writer.WriteString("\r\n")
		return err
	case respArray:
		if reply.null {
			_, err := writer.WriteString("*-1\r\n")
			return err
		}
		if _, err := fmt.Fprintf(writer, "*%d\r\n", len(reply.items)); err != nil {
			return err
		}
		for _, item := range reply.items {
			if err := writeRESP(writer, item); err != nil {
				return err
			}
		}
		return nil
	case respMap:
		if reply.null {
			_, err := writer.WriteString("%-1\r\n")
			return err
		}
		if len(reply.items)%2 != 0 {
			return fmt.Errorf("redis: map requires key/value pairs")
		}
		if _, err := fmt.Fprintf(writer, "%%%d\r\n", len(reply.items)/2); err != nil {
			return err
		}
		for _, item := range reply.items {
			if err := writeRESP(writer, item); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("redis: unsupported response kind %q", reply.kind)
	}
}

func respSimpleString(value string) respValue {
	return respValue{kind: respSimple, data: []byte(value)}
}
func respErrorString(value string) respValue { return respValue{kind: respError, data: []byte(value)} }
func respIntegerValue(value int64) respValue { return respValue{kind: respInteger, value: value} }
func respBulkBytes(value []byte) respValue {
	return respValue{kind: respBulk, data: append([]byte(nil), value...)}
}
func respBulkString(value string) respValue { return respBulkBytes([]byte(value)) }
func respNil() respValue                    { return respValue{kind: respBulk, null: true} }
func respArrayValues(items ...respValue) respValue {
	return respValue{kind: respArray, items: items}
}

func respMapValues(items ...respValue) respValue {
	return respValue{kind: respMap, items: items}
}
