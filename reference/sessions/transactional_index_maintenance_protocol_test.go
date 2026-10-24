//go:build !noservertests

package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalIndexMaintenanceProtocol(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			w := serverAuthenticated(t, f.DSNFor(b))
			peer := serverAuthenticated(t, f.DSNFor(b))
			command := func(w *serverWire, q, tag string, status byte) {
				frames := w.query(t, q)
				serverWantCode(t, frames, "")
				serverWantCompletions(t, frames, tag)
				serverWantReady(t, frames, status)
			}
			defer func() {
				serverWantCode(t, w.query(t, "ROLLBACK"), "")
				serverWantCode(t, peer.query(t, "ROLLBACK"), "")
			}()
			state := func(w *serverWire, action string, changed bool) {
				var options string = "<NULL>"
				if action == "reset" {
					options = "{fillfactor=80}"
				}
				rows := [][]string{{"ix", options}}
				if changed {
					switch action {
					case "rename":
						rows = [][]string{{"fresh", "<NULL>"}}
					case "set":
						rows = [][]string{{"ix", "{fillfactor=80}"}}
					case "reset":
						rows = [][]string{{"ix", "<NULL>"}}
					case "drop":
						rows = nil
					}
				}
				frames := w.query(t, "SELECT relname,reloptions::text FROM pg_class WHERE relnamespace='public'::regnamespace AND relname IN ('ix','fresh') ORDER BY relname")
				serverWantCode(t, frames, "")
				serverWantValues(t, frames, rows)
			}
			for _, action := range []string{"rename", "set", "reset", "drop"} {
				for _, explicit := range []bool{false, true} {
					for _, commit := range []bool{false, true} {
						if !explicit && !commit {
							continue
						}
						command(w, "CREATE TABLE t(n INT)", "CREATE TABLE", 'I')
						command(w, "INSERT INTO t VALUES(1),(2)", "INSERT 0 2", 'I')
						command(w, "CREATE UNIQUE INDEX ix ON t(n)", "CREATE INDEX", 'I')
						if action == "reset" {
							command(w, "ALTER INDEX ix SET(fillfactor=80)", "ALTER INDEX", 'I')
						}
						q, tag := "ALTER INDEX ix RENAME TO fresh", "ALTER INDEX"
						switch action {
						case "set":
							q = "ALTER INDEX ix SET(fillfactor=80)"
						case "reset":
							q = "ALTER INDEX ix RESET(fillfactor)"
						case "drop":
							q, tag = "DROP INDEX ix", "DROP INDEX"
						}
						name := fmt.Sprintf("im_%s_%t_%t", action, explicit, commit)
						// Held named statement is prepared before BEGIN. Describe is syntax-only.
						frames := serverCycle(t, w, serverParse(name, q), serverDescribe('S', name))
						serverWantTags(t, frames, "1tnZ")
						serverWantReady(t, frames, 'I')
						for _, frame := range frames {
							if frame.tag == 't' && (len(frame.body) != 2 || frame.body[0] != 0 || frame.body[1] != 0) {
								t.Fatal("utility Describe returned parameters", frame)
							}
							if frame.tag == 'n' && len(frame.body) != 0 {
								t.Fatal("utility NoData had payload", frame)
							}
						}
						state(w, action, false)
						state(peer, action, false)
						if explicit {
							command(w, "BEGIN", "BEGIN", 'T')
						}
						// Flush without Sync keeps the implicit portal alive while the peer
						// independently observes that Bind/Describe acquired no utility locks.
						w.send(t, serverBind(name, name, nil, nil))
						w.send(t, serverDescribe('P', name))
						w.send(t, serverPacket('H', nil))
						frames = serverThrough(t, w, 'n')
						serverWantTags(t, frames, "2n")
						state(peer, action, false)
						serverScalar(t, peer, "SELECT count(*) FROM pg_locks WHERE locktype='relation' AND mode IN ('ShareUpdateExclusiveLock','AccessExclusiveLock') AND relation IN (SELECT oid FROM pg_class WHERE relnamespace='public'::regnamespace AND relname IN ('ix','t'))", "0")
						frames = serverCycle(t, w, serverExecute(name, 0))
						serverWantTags(t, frames, "CZ")
						serverWantCompletions(t, frames, tag)
						status := byte('I')
						if explicit {
							status = 'T'
						}
						serverWantReady(t, frames, status)
						state(w, action, true)
						state(peer, action, !explicit)
						if explicit {
							if commit {
								command(w, "COMMIT", "COMMIT", 'I')
							} else {
								command(w, "ROLLBACK", "ROLLBACK", 'I')
							}
						}
						state(peer, action, commit)
						command(w, "DROP TABLE t", "DROP TABLE", 'I')
					}
				}
			}
			command(w, "CREATE TABLE t(n INT)", "CREATE TABLE", 'I')
			command(w, "CREATE INDEX ix ON t(n)", "CREATE INDEX", 'I')
			for _, q := range []string{"ALTER INDEX ix RENAME TO fresh; ALTER INDEX fresh SET(fillfactor=80); SELECT 1/0", "ALTER INDEX ix SET(fillfactor=80); ALTER INDEX ix RESET(fillfactor); DROP INDEX ix; SELECT 1/0"} {
				frames := w.query(t, q)
				serverNotificationError(t, frames, "22012", "division by zero", "")
				serverWantReady(t, frames, 'I')
				state(peer, "rename", false)
			}
			frames := w.query(t, "ALTER INDEX ix RENAME TO fresh; SELECT FROM")
			serverWantCode(t, frames, "42601")
			serverWantReady(t, frames, 'I')
			state(peer, "rename", false)
			frames = w.query(t, "DROP INDEX ix,missing; SELECT 42")
			serverNotificationError(t, frames, "42704", `index "missing" does not exist`, "")
			serverWantReady(t, frames, 'I')
			serverWantCompletions(t, frames)
			state(peer, "drop", false)
			frames = w.query(t, "ALTER INDEX ix RENAME TO fresh; SELECT relname FROM pg_class WHERE relname='fresh'")
			serverWantCode(t, frames, "")
			serverWantCompletions(t, frames, "ALTER INDEX", "SELECT 1")
			serverWantValues(t, frames, [][]string{{"fresh"}})
			serverWantReady(t, frames, 'I')
			state(peer, "rename", true)
			command(w, "ALTER INDEX fresh RENAME TO ix", "ALTER INDEX", 'I')
			frames = w.query(t, "ALTER INDEX ix SET(fillfactor=80); COMMIT; ALTER INDEX ix RESET(fillfactor); SELECT 1/0")
			serverNotificationError(t, frames, "22012", "division by zero", "")
			serverWantReady(t, frames, 'I')
			state(peer, "set", true)
			command(w, "ALTER INDEX ix RESET(fillfactor)", "ALTER INDEX", 'I')
			frames = w.query(t, "ALTER INDEX ix SET(fillfactor=80); ALTER INDEX ix RESET(fillfactor); ALTER INDEX ix RENAME TO fresh; DROP INDEX fresh; SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname IN ('ix','fresh')")
			serverWantCode(t, frames, "")
			serverWantCompletions(t, frames, "ALTER INDEX", "ALTER INDEX", "ALTER INDEX", "DROP INDEX", "SELECT 1")
			serverWantValues(t, frames, [][]string{{"0"}})
			serverWantReady(t, frames, 'I')
			state(peer, "drop", true)
			command(w, "CREATE INDEX ix ON t(n)", "CREATE INDEX", 'I')
			// Missing targets are harmless at Parse/Bind/Describe, but fail at Execute.
			command(w, "BEGIN", "BEGIN", 'T')
			command(w, "SAVEPOINT recover", "SAVEPOINT", 'T')
			frames = serverCycle(t, w, serverParse("missing", "ALTER INDEX missing SET(fillfactor=80)"), serverDescribe('S', "missing"), serverBind("missing", "missing", nil, nil), serverDescribe('P', "missing"))
			serverWantTags(t, frames, "1tn2nZ")
			serverWantReady(t, frames, 'T')
			frames = serverCycle(t, w, serverExecute("missing", 0), serverParse("skipped", "SELECT 42"))
			serverWantTags(t, frames, "EZ")
			serverNotificationError(t, frames, "42P01", `relation "missing" does not exist`, "")
			serverWantReady(t, frames, 'E')
			frames = w.query(t, "ALTER INDEX ix SET(fillfactor=80)")
			serverNotificationError(t, frames, "25P02", "current transaction is aborted, commands ignored until end of transaction block", "")
			serverWantReady(t, frames, 'E')
			command(w, "ROLLBACK TO recover", "ROLLBACK", 'T')
			command(w, "ALTER INDEX ix SET(fillfactor=80)", "ALTER INDEX", 'T')
			state(peer, "set", false)
			command(w, "ROLLBACK", "ROLLBACK", 'I')
			state(peer, "set", false)
			command(w, "BEGIN READ ONLY", "BEGIN", 'T')
			frames = serverCycle(t, w, serverParse("readonly", "DROP INDEX missing"), serverDescribe('S', "readonly"), serverBind("readonly", "readonly", nil, nil), serverDescribe('P', "readonly"))
			serverWantTags(t, frames, "1tn2nZ")
			frames = serverCycle(t, w, serverExecute("readonly", 0))
			serverNotificationError(t, frames, "25006", "cannot execute DROP INDEX in a read-only transaction", "")
			serverWantReady(t, frames, 'E')
			command(w, "ROLLBACK", "ROLLBACK", 'I')
			command(w, "INSERT INTO t VALUES(7)", "INSERT 0 1", 'I')
			serverScalar(t, w, "SELECT n FROM t", "7")
			command(w, "DROP TABLE t", "DROP TABLE", 'I')
		})
	}
}
