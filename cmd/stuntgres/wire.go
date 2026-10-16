package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"

	"stuntgres/internal/sqlstate"
)

const (
	maxStartupLength      = 10000 + 4 // Released limit excludes the startup length word.
	maxMessageLength      = (1 << 30) - 2
	maxSmallMessageLength = 10000
)

type frontendMessage struct {
	tag  byte
	body []byte
}

// wireCodec has one reader and one serial writer. It never owns engine leases;
// callers must release them before writing results to the socket.
type wireCodec struct {
	reader        *bufio.Reader
	writer        *bufio.Writer
	startup       []byte
	authenticated bool
	readScratch   [8192]byte // Sole reader's fulfilled-byte growth scratch.
}

func newWire(conn net.Conn) *wireCodec {
	return &wireCodec{reader: bufio.NewReader(conn), writer: bufio.NewWriter(conn)}
}

func framingError(code, message string) error {
	return &sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: code, Message: message}
}

// Lost outer framing is a silent connection retirement, not a SQL diagnostic.
var errInvalidFrame = errors.New("invalid frontend message length")

func frameLength(header []byte, minimum, maximum uint32) (int, error) {
	length := binary.BigEndian.Uint32(header)
	if length < minimum || length > maximum {
		return 0, errInvalidFrame
	}
	return int(length) - 4, nil
}

// readBody grows only after bytes arrive, never from an untrusted announcement.
func (w *wireCodec) readBody(buffer []byte, length int) ([]byte, error) {
	buffer = buffer[:0]
	for len(buffer) < length {
		n := min(length-len(buffer), len(w.readScratch))
		if cap(buffer)-len(buffer) >= n {
			start := len(buffer)
			buffer = buffer[:start+n]
			got, err := io.ReadFull(w.reader, buffer[start:])
			if err != nil {
				return buffer[:start+got], err
			}
		} else {
			got, err := io.ReadFull(w.reader, w.readScratch[:n])
			buffer = append(buffer, w.readScratch[:got]...)
			if err != nil {
				return buffer, err
			}
		}
	}
	return buffer, nil
}

func (w *wireCodec) readStartup() ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(w.reader, header[:]); err != nil {
		return nil, err
	}
	length, err := frameLength(header[:], 8, maxStartupLength)
	if err != nil {
		return nil, err
	}
	w.startup, err = w.readBody(w.startup, length)
	return w.startup, err
}

func (w *wireCodec) readMessage(buffer []byte) (frontendMessage, error) {
	tag, err := w.reader.ReadByte()
	if err != nil {
		return frontendMessage{}, err
	}
	if !knownFrontendTag(tag) && tag != 'X' && !(tag == 'p' && !w.authenticated) {
		return frontendMessage{}, framingError("08P01", fmt.Sprintf("invalid frontend message type %d", tag))
	}
	maximum := uint32(maxSmallMessageLength)
	switch tag {
	case 'P', 'B', 'Q', 'F', 'd':
		maximum = maxMessageLength
	}
	var header [4]byte
	if _, err := io.ReadFull(w.reader, header[:]); err != nil {
		return frontendMessage{}, err
	}
	length, err := frameLength(header[:], 4, maximum)
	if err != nil {
		return frontendMessage{}, err
	}
	buffer, err = w.readBody(buffer, length)
	if err != nil {
		return frontendMessage{}, err
	}
	return frontendMessage{tag: tag, body: buffer}, nil
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
		return "", protocolError("invalid string in message")
	}
	value := string((*body)[:end])
	*body = (*body)[end+1:]
	return value, nil
}
