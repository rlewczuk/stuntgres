package main

import (
	"bufio"
	"context"
	"crypto/md5"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"sort"
	"strings"
	"time"

	"stuntgres/internal/sqlstate"
)

// referenceSession is an authenticated PostgreSQL connection after its first
// ReadyForQuery. The forwarder owns it; pid and key never change.
type referenceSession struct {
	conn   net.Conn
	reader *bufio.Reader
	writer *bufio.Writer
	pid    uint32
	key    []byte
}

// referenceStartup is what the client's startup contributes to the reference
// login: the negotiated protocol and its settings (never user or database).
type referenceStartup struct {
	version  uint32
	settings map[string]string
}

// dialReference connects, authenticates and waits for ReadyForQuery, all
// within timeout. A server ErrorResponse is returned as its diagnostic.
func dialReference(ctx context.Context, c *referenceConfig, timeout time.Duration, startup referenceStartup) (*referenceSession, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", c.address())
	if err != nil {
		return nil, err
	}
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		conn.Close()
		return nil, err
	}
	if c.sslmode != "disable" {
		if conn, err = referenceTLS(conn, c); err != nil {
			return nil, err
		}
	}
	s := &referenceSession{conn: conn, reader: bufio.NewReader(conn), writer: bufio.NewWriter(conn)}
	if err := s.login(c, startup); err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, err
	}
	return s, nil
}

// referenceTLS negotiates SSL; require skips certificate checks like libpq.
func referenceTLS(conn net.Conn, c *referenceConfig) (net.Conn, error) {
	request := appendInt32(appendInt32(nil, 8), sslRequestCode)
	if _, err := conn.Write(request); err != nil {
		conn.Close()
		return nil, err
	}
	var answer [1]byte
	if _, err := io.ReadFull(conn, answer[:]); err != nil {
		conn.Close()
		return nil, err
	}
	if answer[0] != 'S' {
		conn.Close()
		return nil, errors.New("reference server does not support SSL")
	}
	config := &tls.Config{ServerName: c.host, InsecureSkipVerify: c.sslmode == "require"}
	secure := tls.Client(conn, config)
	if err := secure.Handshake(); err != nil {
		conn.Close()
		return nil, err
	}
	return secure, nil
}

func (s *referenceSession) login(c *referenceConfig, startup referenceStartup) error {
	body := appendInt32(nil, startup.version)
	body = appendString(appendString(body, "user"), c.user)
	body = appendString(appendString(body, "database"), c.dbname)
	names := make([]string, 0, len(startup.settings))
	for name := range startup.settings {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		body = appendString(appendString(body, name), startup.settings[name])
	}
	body = append(body, 0)
	if _, err := s.writer.Write(appendInt32(nil, uint32(len(body)+4))); err != nil {
		return err
	}
	if _, err := s.writer.Write(body); err != nil {
		return err
	}
	if err := s.writer.Flush(); err != nil {
		return err
	}
	var scram *scramClient
	var buffer []byte
	for {
		tag, message, err := readBackendMessage(s.reader, buffer)
		if err != nil {
			return err
		}
		buffer = message
		switch tag {
		case 'R':
			if scram, err = s.authenticate(c, message, scram); err != nil {
				return err
			}
		case 'K':
			if len(message) < 8 {
				return errors.New("reference sent a malformed BackendKeyData")
			}
			s.pid = binary.BigEndian.Uint32(message)
			s.key = append([]byte(nil), message[4:]...)
		case 'E':
			return backendError(message)
		case 'Z':
			return nil
		case 'S', 'N', 'v':
			// Startup reports are never captured; the client sees stuntgres's.
		default:
			return fmt.Errorf("reference sent unexpected startup message %q", tag)
		}
	}
}

// authenticate answers one Authentication request and returns the SCRAM
// exchange that continues it, if any.
func (s *referenceSession) authenticate(c *referenceConfig, message []byte, scram *scramClient) (*scramClient, error) {
	if len(message) < 4 {
		return nil, errors.New("reference sent a malformed authentication request")
	}
	code, data := binary.BigEndian.Uint32(message), message[4:]
	password := func() (string, error) {
		value, ok, err := referencePassword(c)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", errors.New("reference server requested a password, but none was supplied")
		}
		return value, nil
	}
	switch code {
	case 0:
		return nil, nil
	case 3:
		value, err := password()
		if err != nil {
			return nil, err
		}
		return nil, s.send('p', appendString(nil, value))
	case 5:
		if len(data) != 4 {
			return nil, errors.New("reference sent a malformed MD5 salt")
		}
		value, err := password()
		if err != nil {
			return nil, err
		}
		inner := md5.Sum([]byte(value + c.user))
		outer := md5.Sum(append([]byte(hex.EncodeToString(inner[:])), data...))
		return nil, s.send('p', appendString(nil, "md5"+hex.EncodeToString(outer[:])))
	case 10:
		if !strings.Contains("\x00"+string(data), "\x00SCRAM-SHA-256\x00") {
			return nil, errors.New("reference server offers no supported SASL mechanism")
		}
		value, err := password()
		if err != nil {
			return nil, err
		}
		if scram, err = newSCRAMClient("", value); err != nil {
			return nil, err
		}
		first := scram.first()
		body := appendString(nil, "SCRAM-SHA-256")
		body = append(appendInt32(body, uint32(len(first))), first...)
		return scram, s.send('p', body)
	case 11:
		if scram == nil {
			return nil, errors.New("reference sent SASLContinue without SASL")
		}
		final, err := scram.final(string(data))
		if err != nil {
			return nil, err
		}
		return scram, s.send('p', []byte(final))
	case 12:
		if scram == nil {
			return nil, errors.New("reference sent SASLFinal without SASL")
		}
		return nil, scram.verify(string(data))
	default:
		return nil, fmt.Errorf("reference requested unsupported authentication method %d", code)
	}
}

func (s *referenceSession) send(tag byte, body []byte) error {
	if err := writeFrame(s.writer, tag, body); err != nil {
		return err
	}
	return s.writer.Flush()
}

// writeFrame writes one tagged protocol message.
func writeFrame(w *bufio.Writer, tag byte, body []byte) error {
	var header [5]byte
	header[0] = tag
	binary.BigEndian.PutUint32(header[1:], uint32(len(body)+4))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err := w.Write(body)
	return err
}

// readBackendMessage reads one backend message into buffer, which may be
// reused by the next call. Announced lengths never allocate ahead of data.
func readBackendMessage(r *bufio.Reader, buffer []byte) (byte, []byte, error) {
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, nil, err
	}
	length := binary.BigEndian.Uint32(header[1:])
	if length < 4 || length > maxMessageLength {
		return 0, nil, errors.New("reference sent an invalid message length")
	}
	buffer = buffer[:0]
	remaining := int64(length - 4)
	for remaining > 0 {
		chunk := int(min(remaining, 8192))
		start := len(buffer)
		buffer = slices.Grow(buffer, chunk)[:start+chunk]
		if _, err := io.ReadFull(r, buffer[start:]); err != nil {
			return 0, nil, err
		}
		remaining -= int64(chunk)
	}
	return header[0], buffer, nil
}

// backendError is a server ErrorResponse as a typed diagnostic.
func backendError(body []byte) error {
	f := errorFields(body)
	return &sqlstate.Error{Severity: f["severity"], SeverityUnlocalized: f["severity_unlocalized"], Code: f["code"], Message: f["message"], Detail: f["detail"], Hint: f["hint"]}
}

// sendReferenceCancel sends a CancelRequest for the session on a new socket.
func sendReferenceCancel(c *referenceConfig, timeout time.Duration, pid uint32, key []byte) error {
	conn, err := net.DialTimeout("tcp", c.address(), timeout)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	packet := appendInt32(nil, uint32(12+len(key)))
	packet = appendInt32(packet, cancelRequestCode)
	packet = appendInt32(packet, pid)
	_, err = conn.Write(append(packet, key...))
	return err
}
