package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestClientEncodingStream(t *testing.T) {
	in, _ := sqltype.RegisteredEncodingConversion("iso8859_1_to_utf8")
	out, _ := sqltype.RegisteredEncodingConversion("utf8_to_iso8859_1")
	policy, err := sqltype.NewClientEncodingPolicy(sqltype.EncodingID("LATIN1"), in, out)
	if err != nil {
		t.Fatal(err)
	}
	conn := framingInput(nil)
	w := newWire(conn)
	d := sqlstate.Error{Severity: "é", SeverityUnlocalized: "é", Code: "é", Message: "é", Detail: "é", Hint: "é", Position: "é", InternalPosition: "é", InternalQuery: "é", Context: "é", Schema: "é", Table: "é", Column: "é", DataType: "é", Constraint: "é", File: "é", Line: "é", Routine: "é"}
	original := d
	body, err := w.encodedDiagnostic(d, "ERROR")
	if err != nil {
		t.Fatal(err)
	}
	for tag, value := range framingFields(t, body) {
		if value != "é" {
			t.Fatalf("preinit field %c=%x", tag, value)
		}
	}
	rawBody, rawErr := w.encodedDiagnostic(sqlstate.Error{Code: "28000", Message: "raw \xff\xe9"}, "FATAL")
	if rawErr != nil || framingFields(t, rawBody)['M'] != "raw \xff\xe9" {
		t.Fatal("preinit raw bytes were decoded or scrubbed", rawErr, rawBody)
	}
	w.encoding, w.encodingReady = policy, true
	body, err = w.encodedDiagnostic(d, "ERROR")
	if err != nil {
		t.Fatal(err)
	}
	fields := framingFields(t, body)
	if len(fields) != 18 {
		t.Fatal("lost diagnostic fields", fields)
	}
	for tag, value := range fields {
		if value != "\xe9" {
			t.Fatalf("normal field %c=%x", tag, value)
		}
	}
	fields = framingFields(t, asciiDiagnostic(d, "ERROR"))
	for tag, value := range fields {
		if value != "??" {
			t.Fatalf("recursion field %c=%x, want per-high-byte replacement", tag, value)
		}
	}
	if !reflect.DeepEqual(d, original) {
		t.Fatal("diagnostic source mutated")
	}
	for _, fatal := range []bool{false, true} {
		conn := framingInput(nil)
		w := newWire(conn)
		w.encoding, w.encodingReady = policy, true
		d := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "P0001", Message: "€", Context: "original context"}
		if err := w.errorResponse(&d, fatal); err != nil {
			t.Fatal(err)
		}
		if err := w.flush(); err != nil {
			t.Fatal(err)
		}
		raw := conn.output.Bytes()
		if len(raw) < 5 || raw[0] != 'E' || int(binary.BigEndian.Uint32(raw[1:])) != len(raw)-1 {
			t.Fatalf("partial/duplicate diagnostic %x", raw)
		}
		severity := "ERROR"
		if fatal {
			severity = "FATAL"
		}
		want := map[byte]string{'S': severity, 'V': severity, 'C': "22P05", 'M': `character with byte sequence 0xe2 0x82 0xac in encoding "UTF8" has no equivalent in encoding "LATIN1"`}
		if got := framingFields(t, raw[5:]); !reflect.DeepEqual(got, want) {
			t.Fatalf("replacement fields %#v want %#v", got, want)
		}
		if d.Message != "€" || d.Context != "original context" {
			t.Fatal("replacement mutated original")
		}
	}
}

func TestClientEncodingStartupNameCapture(t *testing.T) {
	for _, raw := range []string{strings.Repeat("\xe9", 16), strings.Repeat("\xe9", 63), strings.Repeat("\xe9", 64), strings.Repeat("a", 62) + "\xe9tail"} {
		t.Run(fmt.Sprintf("%x", raw), func(t *testing.T) {
			e := engine.New()
			d := database{engine: e, settings: map[string]struct{}{"application_name": {}, "client_encoding": {}}}
			s, notice, err := d.startSession(context.Background(), 1, "postgres", startupDefaults{"application_name": raw, "client_encoding": "LATIN1"})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				s.RequestEnd(session.CleanClose)
				if err := s.WaitEnd(context.Background()); err != nil {
					t.Error(err)
				}
			}()
			want := strings.Repeat(`\xe9`, len(raw))
			switch {
			case len(raw) == 64:
				want = strings.Repeat(`\xe9`, 63)
			case strings.HasPrefix(raw, "a"):
				want = strings.Repeat("a", 62)
			}
			if (notice != nil) != (len(raw) > 63) || s.Settings.Defaults["application_name"] != want ||
				s.Settings.DefaultTyped["application_name"].Text != want || !s.Settings.ProviderDefaults["application_name"] ||
				s.Settings.DefaultSources["application_name"] != session.DefaultSourceClient {
				t.Fatalf("typed startup capture value=%x typed=%x notice=%+v want=%x", s.Settings.Defaults["application_name"], s.Settings.DefaultTyped["application_name"].Text, notice, want)
			}
			retained := s.Settings.DefaultTyped["application_name"]
			s.Settings.Mu.Lock()
			s.Mu.Lock()
			s.Settings.Values["application_name"] = "changed"
			s.Settings.ResetAllLocked(e.Settings)
			got := s.Settings.Value("application_name")
			s.Mu.Unlock()
			s.Settings.Mu.Unlock()
			if got != want || !reflect.DeepEqual(s.Settings.DefaultTyped["application_name"], retained) {
				t.Fatal("reset reparsed expanded NAME or mutated typed capture", got)
			}
		})
	}
}
