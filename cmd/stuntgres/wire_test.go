package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/sqlstate"
)

// framingConn fragments reads without timing or workers, and can model writers
// that violate io.Writer's full-write contract or return a transport error.
type framingConn struct {
	input     *bytes.Reader
	output    bytes.Buffer
	readLimit int
	short     bool
	writeErr  error
}

func (c *framingConn) Read(p []byte) (int, error) {
	if c.readLimit > 0 && len(p) > c.readLimit {
		p = p[:c.readLimit]
	}
	return c.input.Read(p)
}

func (c *framingConn) Write(p []byte) (int, error) {
	if c.writeErr != nil {
		return 0, c.writeErr
	}
	if c.short && len(p) > 0 {
		return c.output.Write(p[:len(p)-1])
	}
	return c.output.Write(p)
}

func (*framingConn) Close() error                     { return nil }
func (*framingConn) LocalAddr() net.Addr              { return framingAddr("local") }
func (*framingConn) RemoteAddr() net.Addr             { return framingAddr("remote") }
func (*framingConn) SetDeadline(time.Time) error      { return nil }
func (*framingConn) SetReadDeadline(time.Time) error  { return nil }
func (*framingConn) SetWriteDeadline(time.Time) error { return nil }

type framingAddr string

func (framingAddr) Network() string  { return "test" }
func (a framingAddr) String() string { return string(a) }

func framingInput(input []byte) *framingConn {
	return &framingConn{input: bytes.NewReader(input), readLimit: 1}
}

func framingPacket(tag byte, length uint32, body []byte) []byte {
	packet := make([]byte, 0, len(body)+5)
	if tag != 0 {
		packet = append(packet, tag)
	}
	packet = appendInt32(packet, length)
	return append(packet, body...)
}

func assertFramingState(t *testing.T, err error, code string) {
	t.Helper()
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != code || d.Severity != "FATAL" || d.SeverityUnlocalized != "FATAL" {
		t.Fatalf("error = %#v; want native FATAL %s", err, code)
	}
}

func framingFields(t *testing.T, body []byte) map[byte]string {
	t.Helper()
	fields := make(map[byte]string)
	for len(body) > 0 && body[0] != 0 {
		tag := body[0]
		body = body[1:]
		value, err := consumeString(&body)
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := fields[tag]; exists {
			t.Fatalf("duplicate diagnostic field %c", tag)
		}
		fields[tag] = value
	}
	if !bytes.Equal(body, []byte{0}) {
		t.Fatalf("invalid diagnostic terminator: %v", body)
	}
	return fields
}

func TestServerFraming(t *testing.T) {
	t.Run("fragmented startup and tagged messages", func(t *testing.T) {
		startup := appendInt32(nil, 196608)
		startup = appendString(startup, "user")
		startup = appendString(startup, "alice")
		startup = append(startup, 0)
		input := framingPacket(0, uint32(len(startup)+4), startup)
		input = append(input, framingPacket('Q', 7, []byte{'x', 'y', 0})...)
		input = append(input, framingPacket('S', 4, nil)...)
		input = append(input, framingPacket('Q', 6, []byte{'z', 0})...)
		w := newWire(framingInput(input))
		got, err := w.readStartup()
		if err != nil || !bytes.Equal(got, startup) {
			t.Fatalf("startup = %v, %v", got, err)
		}
		buffer := make([]byte, 0, 16)
		original := buffer[:cap(buffer)]
		message, err := w.readMessage(buffer)
		if err != nil || message.tag != 'Q' || !bytes.Equal(message.body, []byte{'x', 'y', 0}) {
			t.Fatalf("message = %#v, %v", message, err)
		}
		if &message.body[0] != &original[0] {
			t.Fatal("reader allocated despite sufficient buffer capacity")
		}
		message, err = w.readMessage(message.body)
		if err != nil || message.tag != 'S' || len(message.body) != 0 || cap(message.body) != cap(buffer) {
			t.Fatalf("empty message = %#v, %v", message, err)
		}
		message, err = w.readMessage(message.body)
		if err != nil || &message.body[0] != &original[0] || !bytes.Equal(message.body, []byte{'z', 0}) {
			t.Fatalf("reused message = %#v, %v", message, err)
		}
		if _, err = w.readMessage(message.body); !errors.Is(err, io.EOF) {
			t.Fatalf("end of stream = %v", err)
		}
	})

	t.Run("length bounds precede body allocation", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			tag    byte
			length uint32
			code   string
		}{
			{"startup zero", 0, 0, "08P01"},
			{"startup missing protocol", 0, 7, "08P01"},
			{"startup oversized", 0, maxStartupLength + 1, "54000"},
			{"startup unsigned maximum", 0, ^uint32(0), "54000"},
			{"tagged zero", 'Q', 0, "08P01"},
			{"tagged short", 'Q', 3, "08P01"},
			{"tagged oversized", 'Q', maxMessageLength + 1, "54000"},
			{"tagged unsigned maximum", 'Q', ^uint32(0), "54000"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				w := newWire(framingInput(framingPacket(tc.tag, tc.length, nil)))
				var err error
				if tc.tag == 0 {
					_, err = w.readStartup()
					if cap(w.startup) != 0 {
						t.Fatal("allocated invalid startup body")
					}
				} else {
					_, err = w.readMessage(nil)
				}
				if !errors.Is(err, errInvalidFrame) {
					t.Fatalf("framing error = %v", err)
				}
			})
		}
	})

	t.Run("inclusive maximum lengths", func(t *testing.T) {
		for _, tc := range []struct {
			tag byte
			max int
		}{{0, maxStartupLength}, {'H', maxSmallMessageLength}} {
			body := bytes.Repeat([]byte{'a'}, tc.max-4)
			conn := framingInput(framingPacket(tc.tag, uint32(tc.max), body))
			conn.readLimit = 113
			w := newWire(conn)
			var got []byte
			var err error
			if tc.tag == 0 {
				got, err = w.readStartup()
			} else {
				var message frontendMessage
				message, err = w.readMessage(nil)
				got = message.body
			}
			if err != nil || !bytes.Equal(got, body) {
				t.Fatalf("maximum %d frame: len=%d, err=%v", tc.max, len(got), err)
			}
		}
	})

	t.Run("truncated headers and bodies", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			startup bool
			input   []byte
		}{
			{"startup header", true, []byte{0, 0, 0}},
			{"startup body", true, framingPacket(0, 8, []byte{0, 3})},
			{"tagged header", false, []byte{'Q', 0, 0}},
			{"tagged body", false, framingPacket('Q', 8, []byte{'a'})},
		} {
			t.Run(tc.name, func(t *testing.T) {
				w := newWire(framingInput(tc.input))
				var err error
				if tc.startup {
					_, err = w.readStartup()
				} else {
					_, err = w.readMessage(nil)
				}
				if !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Fatalf("truncation = %v", err)
				}
			})
		}
	})

	t.Run("cstring exact consumption", func(t *testing.T) {
		body := []byte{'a', 0, 0, 'z'}
		value, err := consumeString(&body)
		if err != nil || value != "a" || !bytes.Equal(body, []byte{0, 'z'}) {
			t.Fatalf("first string = %q, %v, remainder %v", value, err, body)
		}
		value, err = consumeString(&body)
		if err != nil || value != "" || !bytes.Equal(body, []byte{'z'}) {
			t.Fatalf("empty string = %q, %v, remainder %v", value, err, body)
		}
		_, err = consumeString(&body)
		if d := diagnostic(err); d.Code != "08P01" || d.Severity == "FATAL" {
			t.Fatalf("logical string error = %v", err)
		}
		if !bytes.Equal(body, []byte{'z'}) {
			t.Fatal("failed consume changed the input")
		}
		body = nil
		_, err = consumeString(&body)
		if d := diagnostic(err); d.Code != "08P01" || d.Severity == "FATAL" {
			t.Fatalf("logical string error = %v", err)
		}
	})

	t.Run("buffered backend frames and integer encoding", func(t *testing.T) {
		conn := framingInput(nil)
		w := newWire(conn)
		body := appendInt16(nil, -1)
		body = appendInt32(body, 0x12345678)
		body = appendString(body, "")
		if !bytes.Equal(body, []byte{255, 255, 0x12, 0x34, 0x56, 0x78, 0}) {
			t.Fatalf("encoded helpers = %v", body)
		}
		if err := w.send('Z', body); err != nil {
			t.Fatal(err)
		}
		if conn.output.Len() != 0 {
			t.Fatal("small response bypassed buffered writer")
		}
		if err := w.flush(); err != nil {
			t.Fatal(err)
		}
		if want := framingPacket('Z', uint32(len(body)+4), body); !bytes.Equal(conn.output.Bytes(), want) {
			t.Fatalf("response = %v, want %v", conn.output.Bytes(), want)
		}
	})

	t.Run("short writes and transport failures", func(t *testing.T) {
		transportErr := errors.New("transport failed")
		for _, tc := range []struct {
			name string
			err  error
		}{{"short write", io.ErrShortWrite}, {"transport error", transportErr}} {
			t.Run(tc.name, func(t *testing.T) {
				conn := framingInput(nil)
				if tc.err == io.ErrShortWrite {
					conn.short = true
				} else {
					conn.writeErr = tc.err
				}
				w := newWire(conn)
				if err := w.send('Z', []byte{'I'}); err != nil {
					t.Fatal(err)
				}
				if err := w.flush(); !errors.Is(err, tc.err) {
					t.Fatalf("flush = %v, want %v", err, tc.err)
				}
				if err := w.send('Z', []byte{'I'}); !errors.Is(err, tc.err) {
					t.Fatalf("write after failure = %v, want %v", err, tc.err)
				}
			})
		}
	})

	t.Run("exact native diagnostics and fatal copy", func(t *testing.T) {
		d := sqlstate.Error{
			Severity: "localized severity", SeverityUnlocalized: "WARNING", Code: "01000", Message: "primary",
			Detail: "detail", Hint: "hint", Position: "7", InternalPosition: "2", InternalQuery: "source",
			Context: "context", Schema: "schema", Table: "table", Column: "column", DataType: "type",
			Constraint: "constraint", File: "file", Line: "42", Routine: "routine",
		}
		original := d
		want := map[byte]string{
			'S': d.Severity, 'V': d.SeverityUnlocalized, 'C': d.Code, 'M': d.Message,
			'D': d.Detail, 'H': d.Hint, 'P': d.Position, 'p': d.InternalPosition, 'q': d.InternalQuery,
			'W': d.Context, 's': d.Schema, 't': d.Table, 'c': d.Column, 'd': d.DataType,
			'n': d.Constraint, 'F': d.File, 'L': d.Line, 'R': d.Routine,
		}
		conn := framingInput(nil)
		w := newWire(conn)
		if err := w.notice(d); err != nil {
			t.Fatal(err)
		}
		if err := w.errorResponse(&d, false); err != nil {
			t.Fatal(err)
		}
		if err := w.errorResponse(&d, true); err != nil {
			t.Fatal(err)
		}
		if d != original {
			t.Fatal("sending diagnostic mutated its source")
		}
		if err := w.flush(); err != nil {
			t.Fatal(err)
		}
		encoded := conn.output.Bytes()
		for i, tag := range []byte{'N', 'E', 'E'} {
			if len(encoded) < 5 || encoded[0] != tag {
				t.Fatalf("diagnostic frame = %v", encoded)
			}
			size := int(binary.BigEndian.Uint32(encoded[1:5])) - 4
			message := frontendMessage{tag: encoded[0], body: encoded[5 : 5+size]}
			encoded = encoded[5+size:]
			if i == 2 {
				want['S'], want['V'] = "FATAL", "FATAL"
			}
			if got := framingFields(t, message.body); !reflect.DeepEqual(got, want) {
				t.Fatalf("diagnostic fields = %#v, want %#v", got, want)
			}
		}
		if err := w.errorResponse(nil, false); err == nil {
			t.Fatal("nil error accepted")
		}
	})

	t.Run("optional fields absent and severity defaults", func(t *testing.T) {
		for _, tc := range []struct {
			severity string
			d        sqlstate.Error
		}{
			{"ERROR", sqlstate.Error{Code: "XX000", Message: "error"}},
			{"NOTICE", sqlstate.Error{Code: "00000", Message: "notice"}},
		} {
			got := framingFields(t, diagnosticBody(tc.d, tc.severity))
			want := map[byte]string{'S': tc.severity, 'V': tc.severity, 'C': tc.d.Code, 'M': tc.d.Message}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("default diagnostic = %#v, want %#v", got, want)
			}
		}
	})
}
