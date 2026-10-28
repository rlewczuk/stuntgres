//go:build !noservertests

package reference_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"reflect"
	"testing"
)

func clientEncodingLogin(t *testing.T, dsn, encoding string) *serverWire {
	t.Helper()
	w := serverDial(t, dsn)
	frames := w.login(t, dsn, 196608, "client_encoding", encoding)
	serverWantCode(t, frames, "")
	if w.statuses["client_encoding"] != encoding {
		t.Fatalf("startup encoding %q: statuses=%v frames=%s", encoding, w.statuses, clientEncodingTranscript(frames))
	}
	return w
}

func clientEncodingTranscript(frames []serverFrame) string {
	var b bytes.Buffer
	for _, f := range frames {
		fmt.Fprintf(&b, "%c[%x]", f.tag, f.body)
		if f.tag == 'E' || f.tag == 'N' {
			fmt.Fprintf(&b, "%q", serverFields(f.body))
		}
	}
	return b.String()
}

// Every portable logical field is exact, including absent fields. Source locations
// are release/build details, but the complete raw body is printed on disagreement.
func clientEncodingDiagnostic(t *testing.T, frames []serverFrame, tag byte, want map[byte]string) {
	t.Helper()
	count := 0
	for _, f := range frames {
		if f.tag != tag {
			continue
		}
		count++
		got := serverFields(f.body)
		for _, key := range []byte("SVCMDHPpqWstcdn") {
			if got[key] != want[key] {
				t.Fatalf("diagnostic %c.%c=%q want %q; actual %s", tag, key, got[key], want[key], clientEncodingTranscript(frames))
			}
		}
		// Check termination/field framing independently of the permissive parser.
		rest := f.body
		for len(rest) > 1 && rest[0] != 0 {
			n := bytes.IndexByte(rest[1:], 0)
			if n < 0 {
				t.Fatalf("unterminated diagnostic: %x", f.body)
			}
			rest = rest[n+2:]
		}
		if !bytes.Equal(rest, []byte{0}) {
			t.Fatalf("diagnostic trailing bytes: %x", f.body)
		}
	}
	if count != 1 {
		t.Fatalf("want one %c: %s", tag, clientEncodingTranscript(frames))
	}
}

func clientEncodingError(t *testing.T, frames []serverFrame, code, message, context string) {
	t.Helper()
	clientEncodingDiagnostic(t, frames, 'E', map[byte]string{'S': "ERROR", 'V': "ERROR", 'C': code, 'M': message, 'W': context})
	serverWantCompletions(t, frames)
}

func clientEncodingCommand(t *testing.T, w *serverWire, q string, tags ...string) []serverFrame {
	t.Helper()
	f := clientEncodingQuery(t, w, q)
	if serverCode(f) != "" {
		t.Fatalf("%s: %s", q, clientEncodingTranscript(f))
	}
	serverWantCompletions(t, f, tags...)
	return f
}

func clientEncodingRow(t *testing.T, f []serverFrame, names []string, oids []uint32, formats []int16, values ...[]byte) {
	t.Helper()
	serverWantRaw(t, f, [][][]byte{values})
	columns := serverColumns(t, f)
	if len(columns) != len(names) {
		t.Fatalf("columns %+v want %v", columns, names)
	}
	for i, c := range columns {
		if c.name != names[i] || c.oid != oids[i] || c.format != formats[i] || c.typmod != -1 || c.table != 0 || c.attribute != 0 {
			t.Fatalf("column[%d]=%+v names=%q oids=%v formats=%v", i, c, names, oids, formats)
		}
		length := int16(-1)
		known := true
		switch c.oid {
		case 16, 18:
			length = 1
		case 23:
			length = 4
		case 2950:
			length = 16
		case 19:
			length = 64
		case 17, 25, 1042, 1043, 114, 3802, 4072, 1790, 1009, 1700:
		default:
			known = false
		}
		if known && c.length != length {
			t.Fatalf("column[%d] length=%d want %d", i, c.length, length)
		}
	}
}

func clientEncodingStatus(t *testing.T, f []serverFrame, name string, want ...string) {
	t.Helper()
	var got []string
	for _, fr := range f {
		if fr.tag == 'S' {
			v := bytes.Split(fr.body, []byte{0})
			if len(v) != 3 || len(v[2]) != 0 {
				t.Fatalf("malformed status %x", fr.body)
			}
			if string(v[0]) == name {
				got = append(got, string(v[1]))
			}
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("status %s=%q want %q: %s", name, got, want, clientEncodingTranscript(f))
	}
}

func clientEncodingCopyBytes(t *testing.T, f []serverFrame, want []byte, count string) {
	t.Helper()
	serverWantCode(t, f, "")
	serverWantCompletions(t, f, count)
	var got []byte
	for _, fr := range f {
		if fr.tag == 'd' {
			got = append(got, fr.body...)
		}
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("COPY bytes %x want %x: %s", got, want, clientEncodingTranscript(f))
	}
}

func clientEncodingBinaryField(value []byte) []byte {
	return append(binary.BigEndian.AppendUint32(nil, uint32(len(value))), value...)
}

func clientEncodingQuery(t *testing.T, w *serverWire, q string) []serverFrame {
	t.Helper()
	w.send(t, serverPacket('Q', serverCString(nil, q)))
	var frames []serverFrame
	for {
		fr, err := w.readFrame()
		if err != nil {
			t.Fatalf("query response %v; actual %s", err, clientEncodingTranscript(frames))
		}
		frames = append(frames, fr)
		if fr.tag == 'Z' {
			return frames
		}
	}
}
