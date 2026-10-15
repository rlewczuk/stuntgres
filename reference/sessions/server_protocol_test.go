//go:build !noservertests

package reference_test

import (
	"bytes"
	"encoding/binary"
	"io"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceServerProtocol(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			serverStartupContracts(t, f, b)
			t.Run("negotiation", func(t *testing.T) {
				w := serverDial(t, dsn)
				w.send(t, serverPut32(serverPut32(nil, 8), 80877103))
				var reply [1]byte
				if _, e := io.ReadFull(w, reply[:]); e != nil {
					t.Fatal(e)
				}
				if reply[0] != 'N' {
					t.Fatalf("SSL reply %q", reply)
				}
				gss := serverDial(t, dsn)
				gss.send(t, serverPut32(serverPut32(nil, 8), 80877104))
				if _, e := io.ReadFull(gss, reply[:]); e != nil {
					t.Fatal(e)
				}
				wantGSS := byte('N')
				if b.PG {
					wantGSS = 'G'
				}
				if reply[0] != wantGSS {
					t.Fatalf("GSS reply %q want %q", reply, wantGSS)
				}
				// G means encryption negotiation must follow. This raw witness has
				// no GSS credentials, so close rather than send plaintext startup.
				gss.Close()
				frames := w.login(t, dsn, 196609, "_pq_.server_contract", "unsupported")
				wantVersion := uint32(196608)
				if b.PG {
					wantVersion = 196609
				}
				if frames[0].tag != 'v' || len(frames[0].body) < 8 || binary.BigEndian.Uint32(frames[0].body) != wantVersion || binary.BigEndian.Uint32(frames[0].body[4:]) != 1 || string(frames[0].body[8:]) != "_pq_.server_contract\x00" {
					t.Fatalf("version negotiation %v", frames)
				}
				serverWantCode(t, frames, "")
				serverScalar(t, w, "SELECT 42", "42")
			})
			t.Run("fragmented_empty", func(t *testing.T) {
				w := serverDial(t, dsn)
				_, user, password, db := serverEndpoint(t, dsn)
				for _, v := range serverStartup(196608, "user", user, "database", db) {
					w.send(t, []byte{v})
				}
				serverWantCode(t, w.authenticate(t, user, password), "")
				for _, q := range []string{"", "-- comment\n", "/* empty */"} {
					frames := w.query(t, q)
					if serverTags(frames) != "IZ" {
						t.Fatalf("empty response %s", serverFrameSummary(frames))
					}
				}
				for _, v := range serverPacket('Q', []byte("SELECT 42\x00")) {
					w.send(t, []byte{v})
				}
				frames := w.ready(t)
				if got := serverValues(t, frames); !reflect.DeepEqual(got, [][]string{{"42"}}) {
					t.Fatal(got)
				}
				w.send(t, serverPacket('X', nil))
				var one [1]byte
				if _, e := w.Read(one[:]); e != io.EOF {
					t.Fatalf("Terminate: %v", e)
				}
			})
			t.Run("extended_cycle", func(t *testing.T) {
				w := serverDial(t, dsn)
				serverWantCode(t, w.login(t, dsn, 196608), "")
				parse := append([]byte{0}, []byte("SELECT $1::integer")...)
				parse = append(parse, 0, 0, 0)
				bind := []byte{0, 0, 0, 0, 0, 1, 0, 0, 0, 2, '4', '2', 0, 0}
				execute := serverPut32([]byte{0}, 0)
				for _, packet := range [][]byte{serverPacket('P', parse), serverPacket('B', bind), serverPacket('D', []byte{'P', 0}), serverPacket('E', execute), serverPacket('S', nil)} {
					w.send(t, packet)
				}
				frames := w.ready(t)
				if b.PG {
					serverWantCode(t, frames, "")
					if !reflect.DeepEqual(serverValues(t, frames), [][]string{{"42"}}) {
						t.Fatal(frames)
					}
					if !strings.HasPrefix(serverTags(frames), "12TDC") {
						t.Fatal(serverTags(frames))
					}
				} else {
					serverWantCode(t, frames, "0A000")
					if serverTags(frames) != "EZ" {
						t.Fatal(serverTags(frames))
					}
					if serverFields(frames[0].body)['M'] != "extended query protocol is not supported by stuntgres Phase 1" {
						t.Fatal(serverFields(frames[0].body))
					}
				}
				serverScalar(t, w, "SELECT 7", "7")
			})
			t.Run("malformed_messages", func(t *testing.T) {
				guard := serverAuthenticated(t, dsn)
				serverWantCode(t, guard.query(t, "CREATE TABLE server_malformed(v integer)"), "")
				cases := []struct {
					name   string
					packet []byte
				}{{"missing_nul", serverPacket('Q', []byte("INSERT INTO server_malformed VALUES (1)"))}, {"trailing_bytes", serverPacket('Q', []byte("INSERT INTO server_malformed VALUES (2)\x00x"))}, {"unknown_tag", serverPacket('?', nil)}, {"copy_data", serverPacket('d', []byte("x"))}, {"short_length", []byte{'Q', 0, 0, 0, 3}}}
				for _, c := range cases {
					t.Run(c.name, func(t *testing.T) {
						w := serverAuthenticated(t, dsn)
						w.send(t, c.packet)
						if b.PG && c.name == "copy_data" {
							serverScalar(t, w, "SELECT 42", "42")
							return
						}
						if b.PG && c.name == "trailing_bytes" {
							frames := w.ready(t)
							serverWantCode(t, frames, "08P01")
							if serverTags(frames) != "EZ" || serverFields(frames[0].body)['S'] != "ERROR" || string(frames[1].body) != "I" {
								t.Fatal(frames)
							}
							serverScalar(t, w, "SELECT 42", "42")
							return
						}
						if b.PG && c.name == "short_length" {
							// PostgreSQL closes an invalid short frame without a
							// tagged diagnostic; native still requires FATAL 08P01.
							if frame, e := w.readFrame(); e != io.EOF {
								t.Fatalf("short frame reply: %v, %v; want EOF", frame, e)
							}
							return
						}
						frame := w.frame(t)
						serverWantCode(t, []serverFrame{frame}, "08P01")
						if !b.PG || c.name != "missing_nul" {
							if serverFields(frame.body)['S'] != "FATAL" {
								t.Fatal(serverFields(frame.body))
							}
						}
					})
				}
				serverScalar(t, guard, "SELECT count(*) FROM server_malformed", "0")
				serverWantCode(t, guard.query(t, "BEGIN; LOCK TABLE server_malformed IN ACCESS EXCLUSIVE MODE; ROLLBACK"), "")
				serverScalar(t, guard, "SELECT 42", "42")
			})
			t.Run("copy_sql", func(t *testing.T) {
				w := serverAuthenticated(t, dsn)
				q := "COPY (SELECT 42) TO STDOUT"
				if b.PG {
					w.send(t, serverPacket('Q', append([]byte(q), 0)))
					frames := w.ready(t)
					if serverTags(frames) != "HdcCZ" {
						t.Fatalf("COPY oracle %s", serverTags(frames))
					}
					var data []byte
					for _, v := range frames {
						if v.tag == 'd' {
							data = append(data, v.body...)
						}
					}
					if string(data) != "42\n" {
						t.Fatalf("COPY data %q", data)
					}
				} else {
					serverWantCode(t, w.query(t, q), "0A000")
				}
				serverScalar(t, w, "SELECT 42", "42")
			})
			t.Run("bounds_and_truncation", func(t *testing.T) {
				if b.PG { // Oracle protocol has a different size limit; assert genuine partial-frame cleanup.
					w := serverAuthenticated(t, dsn)
					w.send(t, []byte{'Q', 0, 0, 0, 20, 'S'})
					w.Close()
					serverScalar(t, serverAuthenticated(t, dsn), "SELECT 42", "42")
					return
				}
				for _, startup := range []bool{false, true} {
					w := serverDial(t, dsn)
					if !startup {
						serverWantCode(t, w.login(t, dsn, 196608), "")
						w.send(t, append([]byte{'Q'}, serverPut32(nil, (16<<20)+1)...))
					} else {
						w.send(t, serverPut32(nil, (64<<10)+1))
					}
					serverWantCode(t, []serverFrame{w.frame(t)}, "54000")
				}
				w := serverAuthenticated(t, dsn)
				w.send(t, []byte{'Q', 0, 0, 0, 20, 'S'})
				w.Close()
				serverScalar(t, serverAuthenticated(t, dsn), "SELECT 42", "42")
			})
		})
	}
}
func serverAuthenticated(t *testing.T, dsn string) *serverWire {
	t.Helper()
	w := serverDial(t, dsn)
	serverWantCode(t, w.login(t, dsn, 196608), "")
	return w
}

func TestReferenceServerAuthentication(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			user := f.UserFor(b)
			password := f.PasswordFor(b)
			database := f.DatabaseFor(b)
			for _, c := range []struct{ name, user, password, db, code string }{{"valid", user, password, database, ""}, {"wrong_password", user, "wrong-password", database, "28P01"}, {"unknown_user", "server_unknown", password, database, "28P01"}, {"missing_database", user, password, "server_absent", "3D000"}, {"denied_database", fixture.ServerGuest, fixture.ServerPassword, fixture.ServerSecondDatabase, "42501"}} {
				t.Run(c.name, func(t *testing.T) {
					dsn := f.AuthDSN(t, b, c.user, c.password, c.db)
					w := serverDial(t, dsn)
					frames := w.login(t, dsn, 196608)
					serverWantCode(t, frames, c.code)
					for _, v := range frames {
						if v.tag != 'R' && v.tag != 'E' && v.tag != 'N' {
							continue
						}
						if bytes.Contains(v.body, []byte("$pbkdf2")) {
							t.Fatal("authentication response leaks verifier")
						}
						// The oracle password equals its username; ParameterStatus
						// and diagnostics may legitimately name that user. Check
						// challenge bytes, not unrelated status fields or random keys.
						if v.tag == 'R' && bytes.Contains(v.body, []byte(c.password)) {
							t.Fatal("authentication challenge leaks password")
						}
						if (v.tag == 'E' || v.tag == 'N') && bytes.Contains(v.body, []byte("wrong-password")) {
							t.Fatal("authentication diagnostic leaks supplied password")
						}
					}
					if c.code == "" {
						serverScalar(t, w, "SELECT session_user", user)
						serverScalar(t, w, "SELECT current_user", user)
					}
				})
			}
			t.Run("default_database", func(t *testing.T) {
				dsn := f.DSNForDatabase(b, user)
				w := serverDial(t, dsn)
				w.send(t, serverStartup(196608, "user", user))
				serverWantCode(t, w.authenticate(t, user, password), "")
				serverScalar(t, w, "SELECT current_database()", user)
			})
			first := serverAuthenticated(t, f.DSNFor(b))
			second := serverAuthenticated(t, f.DSNForDatabase(b, fixture.ServerSecondDatabase))
			serverWantCode(t, first.query(t, "CREATE TABLE server_isolation(v integer); INSERT INTO server_isolation VALUES (11)"), "")
			serverWantCode(t, second.query(t, "CREATE TABLE server_isolation(v integer); INSERT INTO server_isolation VALUES (22)"), "")
			serverScalar(t, first, "SELECT v FROM server_isolation", "11")
			serverScalar(t, second, "SELECT v FROM server_isolation", "22")
			peer := serverAuthenticated(t, f.DSNFor(b))
			serverScalar(t, peer, "SELECT v FROM server_isolation", "11")
		})
	}
}
