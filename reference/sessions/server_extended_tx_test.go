//go:build !noservertests

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceServerExtendedTransactions(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			w := serverAuthenticated(t, f.DSNFor(b))
			peer := serverAuthenticated(t, f.DSNFor(b))
			serverWantCode(t, w.query(t, "CREATE TABLE server_extended_tx(v int)"), "")
			frames := serverCycle(t, w, serverParse("a", "INSERT INTO server_extended_tx VALUES (1)"), serverBind("a", "a", nil, nil), serverExecute("a", 0), serverParse("b", "INSERT INTO server_extended_tx VALUES (2)"), serverBind("b", "b", nil, nil), serverExecute("b", 0), serverParse("bad", "SELECT 1/0"), serverBind("bad", "bad", nil, nil), serverExecute("bad", 0))
			serverWantCode(t, frames, "22012")
			serverWantReady(t, frames, 'I')
			serverScalar(t, peer, "SELECT count(*) FROM server_extended_tx", "0")
			serverWantCode(t, w.query(t, "BEGIN"), "")
			frames = serverCycle(t, w, serverParse("fail", "SELECT 1/0"), serverBind("fail", "fail", nil, nil), serverExecute("fail", 0))
			serverWantCode(t, frames, "22012")
			serverWantReady(t, frames, 'E')
			serverNotificationError(t, w.query(t, "SELECT 42"), "25P02", "current transaction is aborted, commands ignored until end of transaction block", "")
			serverWantCode(t, w.query(t, "ROLLBACK"), "")
			serverScalar(t, w, "SELECT 42", "42")
		})
	}
}
func TestReferenceServerExtendedPipeline(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			w := serverAuthenticated(t, f.DSNFor(b))
			peer := serverAuthenticated(t, f.DSNFor(b))
			serverWantCode(t, w.query(t, "CREATE TABLE server_flush(v int)"), "")
			for _, p := range [][]byte{serverParse("write", "INSERT INTO server_flush VALUES (1)"), serverBind("p", "write", nil, nil), serverExecute("p", 0), serverPacket('H', nil)} {
				w.send(t, p)
			}
			frames := serverThrough(t, w, 'C')
			serverWantTags(t, frames, "12C")
			serverWantCompletions(t, frames, "INSERT 0 1")
			serverScalar(t, peer, "SELECT count(*) FROM server_flush", "0")
			w.send(t, serverPacket('S', nil))
			serverWantTags(t, w.ready(t), "Z")
			serverScalar(t, peer, "SELECT count(*) FROM server_flush", "1")
			for _, p := range [][]byte{serverParse("bad", "SELECT 1/0"), serverBind("bad", "bad", nil, nil), serverExecute("bad", 0), serverBind("skipped", "write", nil, nil), serverExecute("skipped", 0), serverPacket('H', nil), serverPacket('S', nil), serverParse("ok", "SELECT 42"), serverBind("ok", "ok", nil, nil), serverExecute("ok", 0), serverPacket('S', nil)} {
				w.send(t, p)
			}
			frames = w.ready(t)
			serverWantCode(t, frames, "22012")
			serverWantReady(t, frames, 'I')
			serverWantValues(t, w.ready(t), [][]string{{"42"}})
			serverScalar(t, peer, "SELECT count(*) FROM server_flush", "1")
		})
	}
}
func TestReferenceServerExtendedRebind(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			w := serverAuthenticated(t, f.DSNFor(b))
			serverWantCode(t, w.query(t, "CREATE TABLE server_rebind(v int); INSERT INTO server_rebind VALUES (42)"), "")
			serverWantValues(t, serverCycle(t, w, serverParse("s", "SELECT v FROM server_rebind"), serverBind("p", "s", nil, nil), serverExecute("p", 0)), [][]string{{"42"}})
			serverWantCode(t, w.query(t, "ALTER TABLE server_rebind ALTER COLUMN v TYPE text"), "")
			serverNotificationError(t, serverCycle(t, w, serverBind("p", "s", nil, nil)), "0A000", "cached plan must not change result type", "")
			serverWantCode(t, w.query(t, "ALTER TABLE server_rebind ALTER COLUMN v TYPE integer USING v::integer"), "")
			serverWantValues(t, serverCycle(t, w, serverBind("p", "s", nil, nil), serverExecute("p", 0)), [][]string{{"42"}})
		})
	}
}
func TestReferenceServerExtendedACL(t *testing.T) {
	f := fixture.NewServerFixture(t)
	// Altered builtin ACLs are not native logical-Dump inputs. This isolated
	// authority witness disables optional persistence before any SQL; baseline
	// Persistence/RestoredGrants continue to exercise the default dump contract.
	f.Stop(t)
	f.Dumpdir = ""
	f.Restart(t, false)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			admin := serverAuthenticated(t, dsn)
			t.Run("routine_admission", func(t *testing.T) { serverRoutineACL(t, f, b) })
			serverWantCode(t, admin.query(t, "CREATE ROLE server_wire_table_reader"), "")
			defer func() { serverWantCode(t, admin.query(t, "DROP ROLE server_wire_table_reader"), "") }()
			serverWantCode(t, admin.query(t, "CREATE SCHEMA server_wire_acl_schema"), "")
			defer func() { serverWantCode(t, admin.query(t, "DROP SCHEMA server_wire_acl_schema"), "") }()
			serverWantCode(t, admin.query(t, "CREATE TABLE server_wire_acl_schema.server_wire_acl(v int)"), "")
			defer func() { serverWantCode(t, admin.query(t, "DROP TABLE server_wire_acl_schema.server_wire_acl"), "") }()
			serverWantCode(t, admin.query(t, "INSERT INTO server_wire_acl_schema.server_wire_acl VALUES (1),(2); GRANT SELECT ON server_wire_acl_schema.server_wire_acl TO server_wire_table_reader; GRANT USAGE ON SCHEMA server_wire_acl_schema TO server_wire_table_reader"), "")
			guest := serverAuthenticated(t, dsn)
			defer func() {
				serverWantCode(t, guest.query(t, "ROLLBACK"), "")
				guest.Close()
			}()
			serverWantCode(t, guest.query(t, "SET ROLE server_wire_table_reader; BEGIN"), "")
			serverWantCode(t, serverCycle(t, guest, serverParse("s", "SELECT v FROM server_wire_acl_schema.server_wire_acl ORDER BY v"), serverBind("p", "s", nil, nil)), "")
			serverWantCode(t, admin.query(t, "REVOKE SELECT ON server_wire_acl_schema.server_wire_acl FROM server_wire_table_reader"), "")
			serverWantValues(t, serverCycle(t, guest, serverExecute("p", 1)), [][]string{{"1"}})
			serverWantValues(t, serverCycle(t, guest, serverExecute("p", 0)), [][]string{{"2"}})
			serverNotificationError(t, serverCycle(t, guest, serverBind("new", "s", nil, nil)), "42501", "permission denied for table server_wire_acl", "")
			serverWantCode(t, guest.query(t, "ROLLBACK"), "")
		})
	}
}
