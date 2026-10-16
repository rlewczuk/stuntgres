//go:build !noservertests

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceServerPortalCompletion(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			peer := serverAuthenticated(t, dsn)
			serverWantCode(t, peer.query(t, "CREATE TABLE server_portal_completion(v integer)"), "")
			for _, c := range []struct {
				name, sql, tag string
				begin, copy    bool
			}{
				{"nonreturning_insert", "INSERT INTO server_portal_completion VALUES (1)", "INSERT 0 1", true, false},
				{"begin_control", "BEGIN", "BEGIN", false, false},
				{"savepoint_control", "SAVEPOINT completion_mark", "SAVEPOINT", true, false},
				{"copy_input", "COPY server_portal_completion FROM STDIN", "COPY 1", true, true},
			} {
				t.Run(c.name, func(t *testing.T) {
					w := serverAuthenticated(t, dsn)
					defer func() { serverWantCode(t, w.query(t, "ROLLBACK"), "") }()
					if c.begin {
						serverWantCode(t, w.query(t, "BEGIN"), "")
					}
					var frames []serverFrame
					if c.copy {
						for _, packet := range [][]byte{serverParse("s", c.sql), serverBind("p", "s", nil, nil), serverExecute("p", 0), serverPacket('H', nil)} {
							w.send(t, packet)
						}
						serverWantTags(t, serverThrough(t, w, '2'), "12")
						response := w.frame(t)
						if response.tag != 'G' || string(response.body) != "\x00\x00\x01\x00\x00" {
							t.Fatalf("CopyInResponse %c %x", response.tag, response.body)
						}
						w.send(t, serverPacket('d', []byte("7\n")))
						w.send(t, serverPacket('c', nil))
						w.send(t, serverPacket('S', nil))
						frames = w.ready(t)
						serverWantTags(t, frames, "CZ")
					} else {
						frames = serverCycle(t, w, serverParse("s", c.sql), serverBind("p", "s", nil, nil), serverExecute("p", 0))
						serverWantTags(t, frames, "12CZ")
					}
					serverWantCode(t, frames, "")
					serverWantCompletions(t, frames, c.tag)
					serverWantReady(t, frames, 'T')
					serverScalar(t, peer, "SELECT count(*) FROM server_portal_completion", "0")
					frames = serverCycle(t, w, serverExecute("p", 0))
					serverWantTags(t, frames, "EZ")
					serverNotificationError(t, frames, "55000", "portal \"p\" cannot be run", "")
					serverWantReady(t, frames, 'E')
					// A repeated non-row Execute cannot succeed, replay effects, or commit the
					// earlier write. COMMIT on the failed transaction must actually roll back.
					frames = w.query(t, "COMMIT")
					serverWantTags(t, frames, "CZ")
					serverWantCompletions(t, frames, "ROLLBACK")
					serverWantReady(t, frames, 'I')
					serverScalar(t, peer, "SELECT count(*) FROM server_portal_completion", "0")
					serverScalar(t, w, "SELECT 42", "42")
				})
			}
			for _, c := range []struct {
				name, sql, initial, repeat string
				empty, writes              bool
			}{
				{"exhausted_select", "SELECT v FROM (VALUES (1),(2)) AS q(v) ORDER BY v", "SELECT 2", "SELECT 0", false, false},
				{"exhausted_returning", "INSERT INTO server_portal_completion VALUES (1),(2) RETURNING v", "INSERT 0 2", "INSERT 0 0", false, true},
				{"empty_portal", "", "", "", true, false},
			} {
				t.Run(c.name, func(t *testing.T) {
					w := serverAuthenticated(t, dsn)
					defer func() { serverWantCode(t, w.query(t, "ROLLBACK"), "") }()
					serverWantCode(t, peer.query(t, "TRUNCATE server_portal_completion"), "")
					serverWantCode(t, w.query(t, "BEGIN"), "")
					frames := serverCycle(t, w, serverParse("s", c.sql), serverBind("p", "s", nil, nil), serverExecute("p", 0))
					if c.empty {
						serverWantTags(t, frames, "12IZ")
						serverWantCompletions(t, frames)
					} else {
						serverWantTags(t, frames, "12DDCZ")
						serverWantValues(t, frames, [][]string{{"1"}, {"2"}})
						serverWantCompletions(t, frames, c.initial)
					}
					serverWantReady(t, frames, 'T')
					frames = serverCycle(t, w, serverExecute("p", 0))
					if c.empty {
						serverWantTags(t, frames, "IZ")
						serverWantCompletions(t, frames)
					} else {
						serverWantTags(t, frames, "CZ")
						serverWantCompletions(t, frames, c.repeat)
					}
					serverWantCode(t, frames, "")
					serverWantReady(t, frames, 'T')
					if len(serverRawRows(t, frames)) != 0 {
						t.Fatal("exhausted portal replayed rows")
					}
					serverScalar(t, peer, "SELECT count(*) FROM server_portal_completion", "0")
					serverWantCode(t, w.query(t, "COMMIT"), "")
					want := "0"
					if c.writes {
						want = "2"
					}
					serverScalar(t, peer, "SELECT count(*) FROM server_portal_completion", want)
				})
			}
		})
	}
}
