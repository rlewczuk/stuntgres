//go:build !noservertests

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceServerFailedPortalIdentity(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			peer := serverAuthenticated(t, dsn)
			serverWantCode(t, peer.query(t, "CREATE TABLE server_failed_identity(v integer); INSERT INTO server_failed_identity VALUES (0)"), "")
			for _, cancel := range []bool{false, true} {
				name := "row_error"
				if cancel {
					name = "active_copy_cancel"
				}
				t.Run(name, func(t *testing.T) {
					w := serverAuthenticated(t, dsn)
					defer func() { serverWantCode(t, w.query(t, "ROLLBACK"), "") }()
					serverWantCode(t, w.query(t, "BEGIN"), "")
					source := "SELECT 10/v FROM server_failed_identity"
					if cancel {
						source = "COPY server_failed_identity FROM STDIN"
					}
					serverWantTags(t, serverCycle(t, w, serverParse("source", source), serverParse("safe", "SELECT 42"), serverBind("p", "source", nil, nil)), "112Z")
					// The failed owner predates mark; its name survives, unlike the newer owner.
					serverReviewMark(t, w)
					serverWantTags(t, serverCycle(t, w, serverBind("newer", "safe", nil, nil)), "2Z")
					var frames []serverFrame
					if cancel {
						w.send(t, serverExecute("p", 0))
						w.send(t, serverPacket('H', nil))
						response := w.frame(t)
						if response.tag != 'G' || string(response.body) != "\x00\x00\x01\x00\x00" {
							t.Fatalf("active COPY response %c %x", response.tag, response.body)
						}
						serverObserveCopyInput(t, f, b, w)
						// CancelRequest EOF is not an acknowledgement from the target
						// executor. Keep COPY active with one bounded valid row and
						// require its real ErrorResponse before allowing CopyDone.
						serverCancel(t, dsn, w.pid, w.key)
						w.send(t, serverPacket('d', []byte("1\n")))
						frames = serverThrough(t, w, 'E')
						serverWantTags(t, frames, "E")
						serverWantCode(t, frames, "57014")
						// Only after the primary failure may late COPY termination
						// and the future Sync complete extended recovery.
						w.send(t, serverPacket('c', nil))
						w.send(t, serverPacket('S', nil))
						frames = append(frames, w.ready(t)...)
						serverNotificationError(t, frames, "57014", "canceling statement due to user request", "", "COPY server_failed_identity, line 1: \"1\"")
					} else {
						frames = serverCycle(t, w, serverExecute("p", 0))
						serverNotificationError(t, frames, "22012", "division by zero", "")
					}
					serverWantTags(t, frames, "EZ")
					serverWantReady(t, frames, 'E')
					serverWantCompletions(t, frames)
					serverReviewRestore(t, w)
					if cancel {
						// COPY's lock was acquired after mark. Its executor must release it while
						// the older failed portal identity and outer transaction still exist.
						serverWantCode(t, peer.query(t, "SET lock_timeout='1000ms'"), "")
						serverWantCode(t, peer.query(t, "BEGIN; LOCK TABLE server_failed_identity IN ACCESS EXCLUSIVE MODE; ROLLBACK"), "")
					}
					frames = serverCycle(t, w, serverExecute("p", 0))
					serverWantTags(t, frames, "EZ")
					serverNotificationError(t, frames, "55000", "portal \"p\" cannot be run", "")
					serverWantReady(t, frames, 'E')
					serverReviewRestore(t, w)
					frames = serverCycle(t, w, serverBind("p", "safe", nil, nil))
					serverWantTags(t, frames, "EZ")
					serverNotificationError(t, frames, "42P03", "cursor \"p\" already exists", "")
					serverReviewRestore(t, w)
					frames = serverCycle(t, w, serverExecute("newer", 0))
					serverWantTags(t, frames, "EZ")
					serverNotificationError(t, frames, "34000", "portal \"newer\" does not exist", "")
					serverReviewRestore(t, w)
					frames = serverCycle(t, w, serverClose('P', "p"), serverBind("p", "safe", nil, nil), serverExecute("p", 0))
					serverWantTags(t, frames, "32DCZ")
					serverWantValues(t, frames, [][]string{{"42"}})
					serverWantReady(t, frames, 'T')
					serverScalar(t, peer, "SELECT count(*) FROM server_failed_identity", "1")
					serverWantCode(t, w.query(t, "ROLLBACK"), "")
					// Plain SELECT's legitimate Bind-time relation lock belongs to the outer
					// transaction; it must not be confused with leaked failed-executor state.
					serverWantCode(t, peer.query(t, "SET lock_timeout='1000ms'"), "")
					serverWantCode(t, peer.query(t, "BEGIN; LOCK TABLE server_failed_identity IN ACCESS EXCLUSIVE MODE; ROLLBACK"), "")
					serverScalar(t, peer, "SELECT v FROM server_failed_identity", "0")
					serverScalar(t, w, "SELECT 42", "42")
				})
			}
		})
	}
}
