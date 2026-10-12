package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"

	"stuntgres/internal/sqlstate"
)

const (
	maxStartupLength = 64 * 1024
	maxMessageLength = 16 * 1024 * 1024
)

type frontendMessage struct {
	tag  byte
	body []byte
}

// wireCodec has one reader and one serial writer. It never owns engine leases;
// callers must release them before writing results to the socket.
type wireCodec struct {
	reader  *bufio.Reader
	writer  *bufio.Writer
	startup []byte
}

func newWire(conn net.Conn) *wireCodec {
	return &wireCodec{reader: bufio.NewReader(conn), writer: bufio.NewWriter(conn)}
}

func framingError(code, message string) error {
	return &sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: code, Message: message}
}

func frameLength(header []byte, minimum, maximum uint32) (int, error) {
	length := binary.BigEndian.Uint32(header)
	if length < minimum {
		return 0, framingError("08P01", "invalid message length")
	}
	if length > maximum {
		return 0, framingError("54000", "message exceeds maximum allowed length")
	}
	return int(length) - 4, nil
}

// readStartup returns the protocol code and payload, excluding the length word.
// Startup negotiations reuse the allocation but cannot exceed 64 KiB on wire.
func (w *wireCodec) readStartup() ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(w.reader, header[:]); err != nil {
		return nil, err
	}
	length, err := frameLength(header[:], 8, maxStartupLength)
	if err != nil {
		return nil, err
	}
	if cap(w.startup) < length {
		w.startup = make([]byte, length)
	} else {
		w.startup = w.startup[:length]
	}
	if _, err := io.ReadFull(w.reader, w.startup); err != nil {
		return nil, err
	}
	return w.startup, nil
}

// readMessage borrows buffer until the next read. A new allocation occurs only
// when its capacity is too small; callers retain the returned body for reuse.
func (w *wireCodec) readMessage(buffer []byte) (frontendMessage, error) {
	var header [5]byte
	if _, err := io.ReadFull(w.reader, header[:]); err != nil {
		return frontendMessage{}, err
	}
	length, err := frameLength(header[1:], 4, maxMessageLength)
	if err != nil {
		return frontendMessage{}, err
	}
	if cap(buffer) < length {
		buffer = make([]byte, length)
	} else {
		buffer = buffer[:length]
	}
	if _, err := io.ReadFull(w.reader, buffer); err != nil {
		return frontendMessage{}, err
	}
	return frontendMessage{tag: header[0], body: buffer}, nil
}

func (w *wireCodec) send(tag byte, body []byte) error {
	if len(body) > maxMessageLength-4 {
		return framingError("54000", "message exceeds maximum allowed length")
	}
	var header [5]byte
	header[0] = tag
	binary.BigEndian.PutUint32(header[1:], uint32(len(body)+4))
	if _, err := w.writer.Write(header[:]); err != nil {
		return err
	}
	_, err := w.writer.Write(body)
	return err
}

func (w *wireCodec) flush() error { return w.writer.Flush() }

func (w *wireCodec) errorResponse(err error, fatal bool) error {
	d := diagnostic(err)
	if d == nil {
		return errors.New("cannot send a nil SQL error")
	}
	copy := *d
	if fatal {
		copy.Severity = "FATAL"
		copy.SeverityUnlocalized = "FATAL"
	}
	return w.send('E', diagnosticBody(copy, "ERROR"))
}

func (w *wireCodec) notice(d sqlstate.Error) error {
	return w.send('N', diagnosticBody(d, "NOTICE"))
}

func diagnosticBody(d sqlstate.Error, defaultSeverity string) []byte {
	if d.Severity == "" {
		d.Severity = defaultSeverity
	}
	if d.SeverityUnlocalized == "" {
		d.SeverityUnlocalized = d.Severity
	}
	fields := [...]struct {
		tag   byte
		value string
	}{
		{'S', d.Severity}, {'V', d.SeverityUnlocalized}, {'C', d.Code}, {'M', d.Message},
		{'D', d.Detail}, {'H', d.Hint}, {'P', d.Position}, {'p', d.InternalPosition},
		{'q', d.InternalQuery}, {'W', d.Context}, {'s', d.Schema}, {'t', d.Table},
		{'c', d.Column}, {'d', d.DataType}, {'n', d.Constraint}, {'F', d.File},
		{'L', d.Line}, {'R', d.Routine},
	}
	size := 1
	for _, field := range fields {
		if field.value != "" {
			size += len(field.value) + 2
		}
	}
	body := make([]byte, 0, size)
	for _, field := range fields {
		if field.value != "" {
			body = append(body, field.tag)
			body = appendString(body, field.value)
		}
	}
	return append(body, 0)
}

func appendInt16(body []byte, value int16) []byte {
	return binary.BigEndian.AppendUint16(body, uint16(value))
}

func appendInt32(body []byte, value uint32) []byte {
	return binary.BigEndian.AppendUint32(body, value)
}

func appendString(body []byte, value string) []byte {
	body = append(body, value...)
	return append(body, 0)
}

// consumeString consumes one complete C string only. Its caller validates the
// string's meaning and checks for unexpected trailing bytes in the payload.
func consumeString(body *[]byte) (string, error) {
	end := bytes.IndexByte(*body, 0)
	if end < 0 {
		return "", framingError("08P01", "unterminated message string")
	}
	value := string((*body)[:end])
	*body = (*body)[end+1:]
	return value, nil
}
