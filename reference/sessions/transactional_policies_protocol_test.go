//go:build !noservertests

package reference_test

import (
	"fmt"
	"runtime"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalPoliciesProtocol(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"tp_wire_reader"})
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			w := serverAuthenticated(t, f.DSNFor(b))
			peer := serverAuthenticated(t, f.DSNFor(b))
			t.Cleanup(func() {
				w.query(t, "ROLLBACK")
				w.query(t, "RESET ROLE")
				w.query(t, "DEALLOCATE ALL")
				w.query(t, "DROP TABLE IF EXISTS tp_wire")
				w.query(t, "REVOKE USAGE ON SCHEMA public FROM tp_wire_reader")
				w.query(t, "DROP ROLE IF EXISTS tp_wire_reader")
			})
			for _, q := range []string{"CREATE ROLE tp_wire_reader", "GRANT USAGE ON SCHEMA public TO tp_wire_reader"} {
				serverWantCode(t, w.query(t, q), "")
			}
			for _, protocol := range []string{"simple", "named", "unnamed"} {
				for _, ownership := range []string{"autocommit", "commit", "rollback"} {
					t.Run(protocol+"/"+ownership, func(t *testing.T) {
						peerPending := false
						t.Cleanup(func() {
							w.query(t, "ROLLBACK")
							w.query(t, "RESET ROLE")
							w.query(t, "DEALLOCATE ALL")
							if peerPending {
								peer.ready(t)
							}
							peer.query(t, "RESET ROLE")
							w.query(t, "DROP TABLE IF EXISTS tp_wire")
						})
						for _, q := range []string{"CREATE TABLE tp_wire(id INT,allowed BOOL)", "INSERT INTO tp_wire VALUES(1,TRUE),(2,FALSE),(3,NULL)", "GRANT SELECT,INSERT,UPDATE,DELETE ON tp_wire TO tp_wire_reader", "ALTER TABLE tp_wire ENABLE ROW LEVEL SECURITY"} {
							serverWantCode(t, w.query(t, q), "")
						}
						queries := []string{"CREATE POLICY p ON tp_wire TO tp_wire_reader USING(id<=2) WITH CHECK(allowed)", "ALTER POLICY p ON tp_wire USING(id=2)", "ALTER POLICY p ON tp_wire RENAME TO renamed", "DROP POLICY renamed ON tp_wire"}
						tags := []string{"CREATE POLICY", "ALTER POLICY", "ALTER POLICY", "DROP POLICY"}
						// The named statement survives Sync and simple transaction/role
						// controls. It is genuinely held before every definition change.
						if protocol == "named" {
							for i, q := range queries {
								frames := serverCycle(t, w, serverParse(fmt.Sprintf("ddl%d", i), q), serverDescribe('S', fmt.Sprintf("ddl%d", i)))
								serverWantTags(t, frames, "1tnZ")
								serverWantReady(t, frames, 'I')
							}
							frames := serverCycle(t, w, serverParse("held_read", "SELECT id FROM tp_wire ORDER BY id"), serverDescribe('S', "held_read"))
							serverWantTags(t, frames, "1tTZ")
						}
						serverScalar(t, w, "SELECT count(*) FROM pg_depend WHERE classid=3256 AND refobjid='tp_wire'::regclass AND deptype='a'", "0")
						if ownership != "autocommit" {
							serverWantCode(t, w.query(t, "BEGIN"), "")
						}
						ready := byte('I')
						if ownership != "autocommit" {
							ready = 'T'
						}
						for i, q := range queries {
							var frames []serverFrame
							if protocol == "simple" {
								frames = w.query(t, q)
							} else {
								name, portal := fmt.Sprintf("ddl%d", i), fmt.Sprintf("portal%d", i)
								if protocol == "unnamed" {
									frames = serverCycle(t, w, serverParse("", q), serverDescribe('S', ""), serverBind("", "", nil, nil), serverDescribe('P', ""), serverExecute("", 0))
									serverWantTags(t, frames, "1tn2nCZ")
								} else if ownership != "autocommit" {
									frames = serverCycle(t, w, serverBind(portal, name, nil, nil), serverDescribe('P', portal))
									serverWantTags(t, frames, "2nZ")
									serverWantReady(t, frames, 'T')
									// Bind/Describe must not install this next policy step.
									want := "0"
									if i > 0 {
										want = "1"
									}
									serverScalar(t, w, "SELECT count(*) FROM pg_depend WHERE classid=3256 AND refobjid='tp_wire'::regclass AND deptype='a'", want)
									frames = serverCycle(t, w, serverExecute(portal, 0))
									serverWantTags(t, frames, "CZ")
								} else {
									// An autocommit portal ends at Sync, so Bind and Execute
									// deliberately share the same actual protocol cycle.
									frames = serverCycle(t, w, serverBind(portal, name, nil, nil), serverDescribe('P', portal), serverExecute(portal, 0))
									serverWantTags(t, frames, "2nCZ")
								}
							}
							serverWantCode(t, frames, "")
							serverWantCompletions(t, frames, tags[i])
							serverWantReady(t, frames, ready)
							serverWantCode(t, w.query(t, "SET ROLE tp_wire_reader"), "")
							var rows [][]string
							if i == 0 {
								rows = [][]string{{"1"}, {"2"}}
							} else if i < 3 {
								rows = [][]string{{"2"}}
							}
							if protocol == "named" {
								frames = serverCycle(t, w, serverBind("reader", "held_read", nil, nil), serverExecute("reader", 0))
								serverWantCode(t, frames, "")
								serverWantValues(t, frames, rows)
								if ownership != "autocommit" {
									serverWantTags(t, serverCycle(t, w, serverClose('P', "reader")), "3Z")
								}
							} else {
								frames = w.query(t, "SELECT id FROM tp_wire ORDER BY id")
								serverWantCode(t, frames, "")
								serverWantValues(t, frames, rows)
							}
							if ownership != "autocommit" {
								serverScalar(t, peer, "SELECT count(*) FROM pg_depend WHERE classid=3256 AND refobjid='tp_wire'::regclass AND deptype='a'", "0")
							}
							if i == 0 {
								serverWantCode(t, w.query(t, "INSERT INTO tp_wire VALUES(4,TRUE)"), "")
								if ownership != "autocommit" {
									serverWantCode(t, w.query(t, "SAVEPOINT wire_check"), "")
								}
								denied := w.query(t, "INSERT INTO tp_wire VALUES(5,FALSE)")
								serverNotificationError(t, denied, "42501", `new row violates row-level security policy for table "tp_wire"`, "")
								if ownership != "autocommit" {
									serverWantReady(t, denied, 'E')
									serverWantCode(t, w.query(t, "ROLLBACK TO wire_check"), "")
								} else {
									serverWantReady(t, denied, 'I')
								}
							}
							serverWantCode(t, w.query(t, "RESET ROLE"), "")
						}
						if ownership != "autocommit" {
							serverWantCode(t, w.query(t, map[string]string{"commit": "COMMIT", "rollback": "ROLLBACK"}[ownership]), "")
						}
						serverWantCode(t, w.query(t, "BEGIN; CREATE POLICY survivor ON tp_wire TO tp_wire_reader USING(id=1); SAVEPOINT policy_mark; ALTER POLICY survivor ON tp_wire RENAME TO renamed; ALTER POLICY renamed ON tp_wire USING(FALSE)"), "")
						frames := w.query(t, "ALTER POLICY renamed ON tp_wire WITH CHECK(1)")
						serverNotificationError(t, frames, "42804", "argument of POLICY must be type boolean, not type integer", "")
						serverWantReady(t, frames, 'E')
						serverNotificationError(t, w.query(t, "SELECT 1"), "25P02", "current transaction is aborted, commands ignored until end of transaction block", "")
						serverWantCode(t, w.query(t, "ROLLBACK TO policy_mark; SET ROLE tp_wire_reader"), "")
						serverScalar(t, w, "SELECT id FROM tp_wire", "1")
						serverWantCode(t, w.query(t, "RESET ROLE; RELEASE policy_mark; ROLLBACK"), "")
						serverScalar(t, w, "SELECT 1", "1")
						// Surviving changes distinguish real final publication from a
						// block whose create/drop merely cancel each other.
						serverWantCode(t, w.query(t, "BEGIN; CREATE POLICY persisted ON tp_wire TO tp_wire_reader USING(id=2) WITH CHECK(allowed)"), "")
						serverScalar(t, peer, "SELECT count(*) FROM pg_depend WHERE classid=3256 AND refobjid='tp_wire'::regclass AND deptype='a'", "0")
						serverWantCode(t, peer.query(t, "SET ROLE tp_wire_reader"), "")
						peer.send(t, serverPacket('Q', append([]byte("SELECT id FROM tp_wire ORDER BY id"), 0)))
						peerPending = true
						observer := f.RawPool(t, b)
						for {
							var waiting bool
							err := observer.QueryRowContext(f.Ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='relation' AND relation='tp_wire'::regclass::oid AND mode='AccessShareLock' AND NOT granted)").Scan(&waiting)
							if err != nil {
								t.Fatal("wire policy AX barrier", err)
							}
							if waiting {
								break
							}
							runtime.Gosched()
						}
						end := "COMMIT"
						want := "1"
						if ownership == "rollback" {
							end = "ROLLBACK"
							want = "0"
						}
						serverWantCode(t, w.query(t, end), "")
						frames = peer.ready(t)
						peerPending = false
						serverWantCode(t, frames, "")
						var expected [][]string
						if ownership != "rollback" {
							expected = [][]string{{"2"}}
						}
						serverWantValues(t, frames, expected)
						serverWantCode(t, peer.query(t, "RESET ROLE"), "")
						serverScalar(t, peer, "SELECT count(*) FROM pg_depend WHERE classid=3256 AND refobjid='tp_wire'::regclass AND deptype='a'", want)
					})
				}
			}
		})
	}
}
