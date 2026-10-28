package sqltype

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

type encodingChunkReader struct{ chunks []string }

func (r *encodingChunkReader) Read(p []byte) (int, error) {
	for len(r.chunks) > 0 {
		if r.chunks[0] == "" {
			r.chunks = r.chunks[1:]
			continue
		}
		n := copy(p, r.chunks[0])
		r.chunks[0] = r.chunks[0][n:]
		return n, nil
	}
	return 0, io.EOF
}

func TestClientEncodingStream(t *testing.T) {
	for _, w := range []struct{ name, local, unicode string }{
		{"LATIN1", "\xe9", "é"}, {"SJIS", "\x83\x5c\\t", "ソ\\t"},
		{"GB18030", "\x84\x30\x9c\x38", "飯"}, {"EUC_JIS_2004", "\xa5\xf7", "カ゚"},
		{"SQL_ASCII", "é🙂", "é🙂"},
	} {
		p := clientPolicy(t, w.name)
		for _, outbound := range []bool{false, true} {
			raw, want, conversion := w.local, w.unicode, p.Incoming()
			if outbound {
				raw, want, conversion = w.unicode, w.local, p.Outgoing()
			}
			for split := 0; split <= len(raw); split++ {
				r := NewEncodingReader(&encodingChunkReader{[]string{raw[:split], raw[split:]}}, conversion, context.Background)
				got, err := io.ReadAll(r)
				if err != nil || string(got) != want {
					t.Fatalf("%s outbound%t split%d: %x want %x %v", w.name, outbound, split, got, want, err)
				}
			}
		}
	}
	for _, w := range []struct{ name, raw, code, message string }{
		{"SJIS", "ok\n\x82", "22021", `invalid byte sequence for encoding "SJIS": 0x82`},
		{"GB18030", "ok\n\x84\x30\x9c", "22021", `invalid byte sequence for encoding "GB18030": 0x84 0x30 0x9c`},
		{"LATIN1", "ok\n\\.\n\x00", "22021", `invalid byte sequence for encoding "LATIN1": 0x00`},
	} {
		r := NewEncodingReader(strings.NewReader(w.raw), clientPolicy(t, w.name).Incoming(), context.Background)
		var buf [64]byte
		n, err := r.Read(buf[:])
		want := "ok\n"
		if w.name == "LATIN1" {
			want = "ok\n\\.\n"
		}
		if err != nil || string(buf[:n]) != want {
			t.Fatalf("lost consumed prefix %x %v", buf[:n], err)
		}
		n, err = r.Read(buf[:])
		if n != 0 {
			t.Fatalf("failed tail published %x", buf[:n])
		}
		clientPolicyDiagnostic(t, err, w.code, w.message)
	}
	// The same reader has accepted a converted prefix and still owns output;
	// replacing the current context must interrupt buffered work, not just Read.
	ctx := context.Background()
	r := NewEncodingReader(strings.NewReader(strings.Repeat("\xe9", 10000)), clientPolicy(t, "LATIN1").Incoming(), func() context.Context { return ctx })
	var first [1]byte
	n, err := r.Read(first[:])
	if err != nil || n != 1 || first[0] != 0xc3 {
		t.Fatalf("first conversion %x %v", first, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	ctx = canceled
	n, err = r.Read(first[:])
	if n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("buffered context stale %d %v", n, err)
	}
	// Missing default is never repaired by the available kernel tables. Empty
	// input remains empty, as pg_do_encoding_conversion's len<=0 fastpath.
	missing := NewEncodingConversion(6, 7, EncodingConversionHandler{})
	empty, err := io.ReadAll(NewEncodingReader(bytes.NewReader(nil), missing, context.Background))
	if err != nil || len(empty) != 0 {
		t.Fatal(empty, err)
	}
	_, err = io.ReadAll(NewEncodingReader(strings.NewReader("x"), missing, context.Background))
	clientPolicyDiagnostic(t, err, "42883", `default conversion function for encoding "UTF8" to "MULE_INTERNAL" does not exist`)
}
