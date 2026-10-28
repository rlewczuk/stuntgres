//go:build !noservertests

package reference_test

import (
	"bytes"
	"encoding/binary"
	"io"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceClientEncodingStartup(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			for _, c := range []struct{ encoding, raw string }{{"LATIN1", "\xe9"}, {"SJIS", "\x82\xa0"}, {"SQL_ASCII", "é"}} {
				t.Run(c.encoding, func(t *testing.T) {
					w := clientEncodingLogin(t, dsn, c.encoding)
					frames := clientEncodingQuery(t, w, "SELECT '"+c.raw+"'::text AS v")
					serverWantTags(t, frames, "TDCZ")
					clientEncodingRow(t, frames, []string{"v"}, []uint32{25}, []int16{0}, []byte(c.raw))
					serverWantCompletions(t, frames, "SELECT 1")
				})
			}
			for _, c := range []struct {
				name                  string
				extra                 []string
				encoding, application string
			}{
				{"last_wins", []string{"client_encoding", "SJIS", "client_encoding", "LATIN1"}, "LATIN1", ""},
				{"options_before_direct", []string{"options", "-c client_encoding=SJIS", "client_encoding", "LATIN1"}, "LATIN1", ""},
				{"raw_app", []string{"client_encoding", "LATIN1", "application_name", "\xe9"}, "LATIN1", `\xe9`},
				{"utf8_app", []string{"client_encoding", "LATIN1", "application_name", "é"}, "LATIN1", `\xc3\xa9`},
				{"options_raw_app", []string{"options", "-c application_name=\xe9 -c client_encoding=LATIN1"}, "LATIN1", `\xe9`},
			} {
				t.Run(c.name, func(t *testing.T) {
					w := serverDial(t, dsn)
					frames := w.login(t, dsn, 196608, c.extra...)
					if serverCode(frames) != "" || w.statuses["client_encoding"] != c.encoding || w.statuses["application_name"] != c.application {
						t.Fatalf("startup actual=%s statuses=%q", clientEncodingTranscript(frames), w.statuses)
					}
					serverScalar(t, w, "SELECT 42", "42")
				})
			}
			t.Run("raw_negotiation_name", func(t *testing.T) {
				w := serverDial(t, dsn)
				frames := w.login(t, dsn, 196611, "client_encoding", "LATIN1", "_pq_.\xe9", "x")
				serverWantCode(t, frames, "")
				want := serverPut32(serverPut32(nil, 196610), 1)
				want = serverCString(want, "_pq_.\xe9")
				if len(frames) == 0 || frames[0].tag != 'v' || !bytes.Equal(frames[0].body, want) {
					t.Fatalf("raw negotiation %s want %x", clientEncodingTranscript(frames), want)
				}
			})
			for _, c := range []struct{ name, user, password, db, options, code, message string }{
				{name: "wrong_password", password: "wrong\xff", code: "28P01"},
				{name: "raw_user", user: "missing\xe9", password: "wrong", code: "28P01", message: "password authentication failed for user \"missing\xe9\""},
				{name: "byte_clipped_user", user: strings.Repeat("x", 62) + "é_tail", password: "wrong", code: "28P01", message: "password authentication failed for user \"" + strings.Repeat("x", 62) + "\xc3\""},
				{name: "raw_database", db: "missing\xe9", code: "3D000", message: "database \"missing\xe9\" does not exist"},
				{name: "raw_unknown_option", options: "-c missing\xe9=value", code: "42704", message: "unrecognized configuration parameter \"missing\xe9\""},
				{name: "invalid_options", options: "--missing\xe9=value", code: "42704", message: "unrecognized configuration parameter \"missing\xe9\""},
			} {
				t.Run(c.name, func(t *testing.T) {
					_, user, password, db := serverEndpoint(t, dsn)
					if c.user != "" {
						user = c.user
					}
					if c.password != "" {
						password = c.password
					}
					if c.db != "" {
						db = c.db
					}
					w := serverDial(t, dsn)
					pairs := []string{"user", user, "database", db, "client_encoding", "LATIN1"}
					if c.options != "" {
						pairs = append(pairs, "options", c.options)
					}
					w.send(t, serverStartup(196608, pairs...))
					frames := w.authenticate(t, user, password)
					message := c.message
					if message == "" {
						message = "password authentication failed for user \"" + user + "\""
					}
					clientEncodingDiagnostic(t, frames, 'E', map[byte]string{'S': "FATAL", 'V': "FATAL", 'C': c.code, 'M': message})
					for _, fr := range frames {
						if fr.tag == 'S' || fr.tag == 'K' || fr.tag == 'Z' {
							t.Fatalf("failed startup publication: %s", clientEncodingTranscript(frames))
						}
					}
					if _, err := w.readFrame(); err != io.EOF {
						t.Fatalf("failed startup must close, got %v: %s", err, clientEncodingTranscript(frames))
					}
				})
			}
			for _, c := range []struct{ encoding, code, message string }{
				{"MULE_INTERNAL", "0A000", "conversion between MULE_INTERNAL and UTF8 is not supported"},
				{"not_an_encoding", "22023", `invalid value for parameter "client_encoding": "not_an_encoding"`},
			} {
				t.Run(c.encoding+"_failure", func(t *testing.T) {
					w := serverDial(t, dsn)
					frames := w.login(t, dsn, 196608, "client_encoding", c.encoding)
					clientEncodingDiagnostic(t, frames, 'E', map[byte]string{'S': "FATAL", 'V': "FATAL", 'C': c.code, 'M': c.message})
					for _, fr := range frames {
						if fr.tag == 'S' || fr.tag == 'K' || fr.tag == 'Z' {
							t.Fatalf("unusable startup published: %s", clientEncodingTranscript(frames))
						}
					}
					if len(w.key) != 0 || w.pid != 0 {
						t.Fatal("failed startup registered cancellation key")
					}
				})
			}
			// A startup key/value is terminated text, not a counted Bind datum.
			t.Run("raw_framing", func(t *testing.T) {
				w := serverDial(t, dsn)
				packet := serverStartup(196608, "user", f.UserFor(b), "client_encoding", "LATIN1")
				packet = packet[:len(packet)-1]
				binary.BigEndian.PutUint32(packet, uint32(len(packet)))
				w.send(t, packet)
				frames := []serverFrame{w.frame(t)}
				serverWantCode(t, frames, "08P01")
			})
		})
	}
}
