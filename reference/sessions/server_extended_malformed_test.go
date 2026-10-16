//go:build !noservertests

package reference_test

import (
	"io"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceServerExtendedMalformed(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			for _, c := range []struct {
				name    string
				packet  []byte
				message string
			}{
				{"parse_string", serverPacket('P', []byte("s")), "invalid string in message"},
				{"parse_count", serverPacket('P', []byte{0, 0, 0}), "insufficient data left in message"},
				{"describe_missing", serverPacket('D', nil), "no data left in message"},
				{"describe_kind", serverPacket('D', []byte{'?', 0}), "invalid DESCRIBE message subtype 63"},
				{"close_kind", serverPacket('C', []byte{'?', 0}), "invalid CLOSE message subtype 63"},
				{"execute_short", serverPacket('E', []byte{0, 0}), "insufficient data left in message"},
				{"flush_tail", serverPacket('H', []byte{0}), "invalid message format"},
			} {
				t.Run(c.name, func(t *testing.T) {
					w := serverAuthenticated(t, dsn)
					w.send(t, c.packet)
					w.send(t, serverParse("skipped", "SELECT 99"))
					w.send(t, serverPacket('H', nil))
					w.send(t, serverPacket('S', nil))
					frames := w.ready(t)
					serverWantTags(t, frames, "EZ")
					serverNotificationError(t, frames, "08P01", c.message, "")
					serverScalar(t, w, "SELECT 42", "42")
				})
			}
			for _, tag := range []byte{'d', 'c', 'f'} {
				w := serverAuthenticated(t, dsn)
				w.send(t, serverPacket(tag, []byte{0}))
				serverScalar(t, w, "SELECT 42", "42")
			}
			t.Run("unknown_tag_fatal", func(t *testing.T) {
				w := serverAuthenticated(t, dsn)
				w.send(t, []byte{'?'})
				fr := w.frame(t)
				fields := serverFields(fr.body)
				if fr.tag != 'E' || fields['S'] != "FATAL" || fields['V'] != "FATAL" || fields['C'] != "08P01" || fields['M'] != "invalid frontend message type 63" {
					t.Fatal(fields)
				}
				if _, err := w.readFrame(); err != io.EOF {
					t.Fatalf("fatal EOF %v", err)
				}
			})
			t.Run("fragment_coalesce", func(t *testing.T) {
				w := serverAuthenticated(t, dsn)
				packets := append(append(serverParse("s", "SELECT $1::integer"), serverBind("p", "s", nil, [][]byte{[]byte("42")})...), serverExecute("p", 0)...)
				packets = append(packets, serverPacket('S', nil)...)
				for _, v := range packets {
					w.send(t, []byte{v})
				}
				frames := w.ready(t)
				serverWantTags(t, frames, "12DCZ")
				serverWantValues(t, frames, [][]string{{"42"}})
			})
		})
	}
}
func TestReferenceServerVersionCancellation(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			serverStartupBoundaries(t, dsn)
			for _, version := range []uint32{196608, 196609, 196610, 196611} {
				for _, options := range []bool{false, true} {
					w := serverDial(t, dsn)
					var extras []string
					if options {
						extras = []string{"_pq_.first", "1", "_pq_.second", "2"}
					}
					frames := w.login(t, dsn, version, extras...)
					serverWantCode(t, frames, "")
					negotiated := version
					if negotiated > 196610 {
						negotiated = 196610
					}
					keylen := 4
					if negotiated >= 196610 {
						keylen = 32
					}
					if len(w.key) != keylen {
						t.Fatalf("version %d key length %d want %d", version, len(w.key), keylen)
					}
					count := 0
					for _, fr := range frames {
						if fr.tag == 'v' {
							count++
							want := serverPut32(nil, negotiated)
							if options {
								want = serverPut32(want, 2)
								want = append(want, []byte("_pq_.first\x00_pq_.second\x00")...)
							} else {
								want = serverPut32(want, 0)
							}
							if string(fr.body) != string(want) {
								t.Fatalf("negotiation %x want %x", fr.body, want)
							}
						}
					}
					if (options || version > 196610) != (count == 1) {
						t.Fatalf("negotiation count %d", count)
					}
					for _, n := range []int{0, 1, 3, 4, 32, 256, 257} {
						key := make([]byte, n)
						copy(key, w.key)
						if len(key) > 0 {
							key[len(key)-1] ^= 1
						}
						serverCancel(t, dsn, w.pid, key)
						serverScalar(t, w, "SELECT 42", "42")
					}
					serverCancel(t, dsn, 0, w.key)
					serverCancel(t, dsn, w.pid, w.key)
					serverScalar(t, w, "SELECT 42", "42")
				}
			}
			w := serverDial(t, dsn)
			w.send(t, serverStartup(4<<16, "user", f.UserFor(b)))
			fr := w.frame(t)
			fields := serverFields(fr.body)
			if fields['S'] != "FATAL" || fields['C'] != "0A000" || fields['M'] != "unsupported frontend protocol 4.0: server supports 3.0 to 3.2" {
				t.Fatal(fields)
			}
		})
	}
}
