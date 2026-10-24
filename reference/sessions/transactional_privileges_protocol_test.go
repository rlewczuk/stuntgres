//go:build !noservertests

package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalPrivilegesProtocol(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"tx_wire_reader"})
	f := fixture.NewServerFixture(t)
	f.Stop(t)
	f.Dumpdir = ""
	f.Restart(t, false)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			w, peer := serverAuthenticated(t, f.DSNFor(b)), serverAuthenticated(t, f.DSNFor(b))
			defer func() { serverWantCode(t, w.query(t, "ROLLBACK"), "") }()
			for _, q := range []string{"CREATE ROLE tx_wire_reader", "GRANT USAGE ON SCHEMA public TO tx_wire_reader", "CREATE TABLE tx_wire_acl(a INT)", "INSERT INTO tx_wire_acl VALUES(7)"} {
				serverWantCode(t, w.query(t, q), "")
			}
			serverWantCode(t, peer.query(t, "SET ROLE tx_wire_reader"), "")
			for _, k := range []struct {
				name, q, tag, baseline string
				before, after          bool
			}{
				{"grant", "GRANT SELECT ON tx_wire_acl TO tx_wire_reader", "GRANT", "REVOKE SELECT ON tx_wire_acl FROM tx_wire_reader", false, true},
				{"revoke", "REVOKE SELECT ON tx_wire_acl FROM tx_wire_reader", "REVOKE", "GRANT SELECT ON tx_wire_acl TO tx_wire_reader", true, false},
			} {
				serverWantCode(t, w.query(t, k.baseline), "")
				frames := serverCycle(t, w, serverParse(k.name, k.q), serverDescribe('S', k.name))
				serverWantTags(t, frames, "1tnZ")
				serverWantReady(t, frames, 'I')
				probe := func(client *serverWire, allow bool) {
					if allow {
						serverScalar(t, client, "SELECT a FROM tx_wire_acl", "7")
					} else {
						serverNotificationError(t, client.query(t, "SELECT a FROM tx_wire_acl"), "42501", "permission denied for table tx_wire_acl", "")
					}
				}
				probe(peer, k.before)
				for _, commit := range []bool{false, true} {
					serverWantCode(t, w.query(t, "BEGIN"), "")
					frames = serverCycle(t, w, serverBind(k.name+"_portal", k.name, nil, nil), serverDescribe('P', k.name+"_portal"))
					serverWantTags(t, frames, "2nZ")
					serverWantReady(t, frames, 'T')
					probe(peer, k.before)
					frames = serverCycle(t, w, serverExecute(k.name+"_portal", 0))
					serverWantTags(t, frames, "CZ")
					serverWantReady(t, frames, 'T')
					transactionalPrivilegesTag(t, frames, k.tag)
					serverWantCode(t, w.query(t, "SAVEPOINT wire_probe"), "")
					serverWantCode(t, w.query(t, "SET ROLE tx_wire_reader"), "")
					probe(w, k.after)
					if !k.after {
						serverWantReady(t, w.query(t, "SELECT 1"), 'E')
					}
					serverWantCode(t, w.query(t, "ROLLBACK TO wire_probe"), "")
					probe(peer, k.before)
					ending := "ROLLBACK"
					if commit {
						ending = "COMMIT"
					}
					frames = w.query(t, ending)
					serverWantCode(t, frames, "")
					serverWantReady(t, frames, 'I')
					allowed := k.before
					if commit {
						allowed = k.after
					}
					probe(peer, allowed)
				}
			}
			serverWantCode(t, w.query(t, "REVOKE SELECT ON tx_wire_acl FROM tx_wire_reader"), "")
			frames := w.query(t, "GRANT SELECT ON tx_wire_acl TO tx_wire_reader; INSERT INTO tx_wire_acl VALUES(9); SELECT missing")
			serverWantCode(t, frames, "42703")
			serverWantReady(t, frames, 'I')
			serverNotificationError(t, peer.query(t, "SELECT a FROM tx_wire_acl"), "42501", "permission denied for table tx_wire_acl", "")
			serverScalar(t, w, "SELECT count(*) FROM tx_wire_acl", "1")
			q := "ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO tx_wire_reader"
			frames = serverCycle(t, w, serverParse("defaults", q), serverDescribe('S', "defaults"))
			serverWantTags(t, frames, "1tnZ")
			serverWantCode(t, w.query(t, "BEGIN"), "")
			frames = serverCycle(t, w, serverBind("defaults_portal", "defaults", nil, nil), serverDescribe('P', "defaults_portal"))
			serverWantTags(t, frames, "2nZ")
			frames = serverCycle(t, w, serverExecute("defaults_portal", 0))
			serverWantTags(t, frames, "CZ")
			serverWantReady(t, frames, 'T')
			transactionalPrivilegesTag(t, frames, "ALTER DEFAULT PRIVILEGES")
			serverWantCode(t, w.query(t, "CREATE TABLE tx_wire_default(a INT); INSERT INTO tx_wire_default VALUES(11); SET ROLE tx_wire_reader"), "")
			serverScalar(t, w, "SELECT a FROM tx_wire_default", "11")
			serverScalar(t, peer, "SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='tx_wire_default'", "0")
			serverWantCode(t, w.query(t, "ROLLBACK"), "")
			serverScalar(t, peer, "SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='tx_wire_default'", "0")
			frames = w.query(t, q+"; CREATE TABLE tx_wire_late_default(a INT); INSERT INTO tx_wire_late_default VALUES(11); SELECT missing")
			serverWantCode(t, frames, "42703")
			serverWantReady(t, frames, 'I')
			serverScalar(t, peer, "SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='tx_wire_late_default'", "0")
			serverWantCode(t, w.query(t, "CREATE TABLE tx_wire_after_failure(a INT); INSERT INTO tx_wire_after_failure VALUES(9)"), "")
			serverNotificationError(t, peer.query(t, "SELECT a FROM tx_wire_after_failure"), "42501", "permission denied for table tx_wire_after_failure", "")
			serverWantCode(t, w.query(t, "BEGIN READ ONLY"), "")
			frames = serverCycle(t, w, serverBind("readonly_defaults", "defaults", nil, nil), serverExecute("readonly_defaults", 0))
			serverNotificationError(t, frames, "25006", "cannot execute ALTER DEFAULT PRIVILEGES in a read-only transaction", "")
			serverWantReady(t, frames, 'E')
			frames = w.query(t, "ROLLBACK")
			serverWantCode(t, frames, "")
			serverWantReady(t, frames, 'I')
			serverWantCode(t, w.query(t, "BEGIN; "+q+"; CREATE TABLE tx_wire_default(a INT); INSERT INTO tx_wire_default VALUES(13); COMMIT"), "")
			defer func() {
				serverWantCode(t, w.query(t, "ALTER DEFAULT PRIVILEGES REVOKE SELECT ON TABLES FROM tx_wire_reader"), "")
			}()
			serverScalar(t, peer, "SELECT a FROM tx_wire_default", "13")
			serverScalar(t, w, "SELECT 1", "1")
		})
	}
}

func transactionalPrivilegesTag(t *testing.T, frames []serverFrame, want string) {
	t.Helper()
	n := 0
	for _, f := range frames {
		if f.tag == 'C' {
			n++
			if string(f.body) != want+"\x00" {
				t.Fatalf("completion %q want %q", f.body, want)
			}
		}
	}
	if n != 1 {
		t.Fatalf("completion count %d want 1", n)
	}
}
