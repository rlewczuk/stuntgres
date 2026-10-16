//go:build !noservertests

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// REL_18_0 postgres.c start_xact_command promotes the transaction after
// completed/suspended Execute (XACT_FLAGS_PIPELINING). F and single/empty Q
// finish a command, whereas multi-statement Q also ends the implicit block.
func TestReferenceServerMixedCycles(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			peer := serverAuthenticated(t, dsn)
			serverWantCode(t, peer.query(t, "CREATE TABLE server_mixed_cycle(v integer)"), "")
			for _, explicit := range []bool{false, true} {
				for _, stage := range []string{"bound", "completed", "suspended"} {
					for _, cross := range []string{"function", "single_query", "multiple_query", "empty_query"} {
						name := stage + "/" + cross
						if explicit {
							name = "explicit/" + name
						}
						t.Run(name, func(t *testing.T) {
							w := serverAuthenticated(t, dsn)
							defer w.Close()
							serverWantCode(t, peer.query(t, "TRUNCATE server_mixed_cycle"), "")
							if explicit {
								serverWantCode(t, w.query(t, "BEGIN"), "")
							}
							for _, packet := range [][]byte{serverParse("insert", "INSERT INTO server_mixed_cycle VALUES (1),(2) RETURNING v"), serverBind("p", "insert", nil, nil), serverPacket('H', nil)} {
								w.send(t, packet)
							}
							serverWantTags(t, serverThrough(t, w, '2'), "12")
							executed := stage != "bound"
							if executed {
								limit := uint32(0)
								end := byte('C')
								tags := "DDC"
								wantRows := [][]string{{"1"}, {"2"}}
								if stage == "suspended" {
									limit = 1
									end = 's'
									tags = "Ds"
									wantRows = wantRows[:1]
								}
								w.send(t, serverExecute("p", limit))
								w.send(t, serverPacket('H', nil))
								frames := serverThrough(t, w, end)
								serverWantTags(t, frames, tags)
								serverWantValues(t, frames, wantRows)
								if stage == "completed" {
									serverWantCompletions(t, frames, "INSERT 0 2")
								}
							}
							serverScalar(t, peer, "SELECT count(*) FROM server_mixed_cycle", "0")
							var frames []serverFrame
							switch cross {
							case "function":
								w.send(t, serverFunction(1397, nil, [][]byte{[]byte("-42")}, 0))
								frames = w.ready(t)
								serverWantTags(t, frames, "VZ")
								if string(frames[0].body) != "\x00\x00\x00\x0242" {
									t.Fatalf("fastpath result %x", frames[0].body)
								}
							case "single_query":
								frames = w.query(t, "SELECT 42")
								serverWantTags(t, frames, "TDCZ")
								serverWantValues(t, frames, [][]string{{"42"}})
							case "multiple_query":
								frames = w.query(t, "SELECT 42; SELECT 43")
								serverWantTags(t, frames, "TDCTDCZ")
								serverWantValues(t, frames, [][]string{{"42"}, {"43"}})
							case "empty_query":
								frames = w.query(t, "")
								serverWantTags(t, frames, "IZ")
							}
							retained := explicit || (executed && cross != "multiple_query")
							ready := byte('I')
							if retained {
								ready = 'T'
							}
							serverWantReady(t, frames, ready)
							published := "0"
							if executed && !retained {
								published = "2"
							}
							serverScalar(t, peer, "SELECT count(*) FROM server_mixed_cycle", published)
							w.send(t, serverDescribe('P', "p"))
							w.send(t, serverPacket('H', nil))
							if retained {
								serverWantTags(t, serverThrough(t, w, 'T'), "T")
							} else {
								failure := serverThrough(t, w, 'E')
								serverWantTags(t, failure, "E")
								serverNotificationError(t, failure, "34000", "portal \"p\" does not exist", "")
							}
							w.send(t, serverPacket('S', nil))
							boundary := w.ready(t)
							serverWantTags(t, boundary, "Z")
							if explicit {
								serverWantReady(t, boundary, 'T')
								serverWantTags(t, serverCycle(t, w, serverDescribe('P', "p")), "TZ")
								serverWantCode(t, w.query(t, "COMMIT"), "")
							} else {
								serverWantReady(t, boundary, 'I')
							}
							finalCount := "0"
							if executed {
								finalCount = "2"
							}
							serverScalar(t, peer, "SELECT count(*) FROM server_mixed_cycle", finalCount)
							serverNotificationError(t, serverCycle(t, w, serverDescribe('P', "p")), "34000", "portal \"p\" does not exist", "")
							serverScalar(t, w, "SELECT 42", "42")
						})
					}
				}
			}
			t.Run("recovery_skips_function_and_query", func(t *testing.T) {
				w := serverAuthenticated(t, dsn)
				defer w.Close()
				for _, packet := range [][]byte{serverParse("bad", "SELECT 1/0"), serverBind("bad", "bad", nil, nil), serverExecute("bad", 0), serverFunction(1397, nil, [][]byte{[]byte("-42")}, 0), serverPacket('Q', serverCString(nil, "INSERT INTO server_mixed_cycle VALUES (99)")), serverPacket('H', nil), serverPacket('S', nil)} {
					w.send(t, packet)
				}
				frames := w.ready(t)
				serverWantCode(t, frames, "22012")
				serverWantReady(t, frames, 'I')
				for _, fr := range frames {
					if fr.tag == 'V' || fr.tag == 'C' || fr.tag == 'D' {
						t.Fatalf("work escaped skip-until-Sync: %s", serverTags(frames))
					}
				}
				serverScalar(t, peer, "SELECT count(*) FROM server_mixed_cycle WHERE v=99", "0")
				serverScalar(t, w, "SELECT 42", "42")
			})
		})
	}
}
