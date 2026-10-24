//go:build !noservertests

package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalCommentProtocol(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"tx_wire_comment_role"})
	fixture.SchemaReferenceCleanup(t, "tx_wire_comment_schema")
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			w, peer := serverAuthenticated(t, f.DSNFor(b)), serverAuthenticated(t, f.DSNFor(b))
			defer func() { serverWantCode(t, w.query(t, "ROLLBACK"), "") }()
			for _, q := range []string{"CREATE TABLE tx_wire_comment(a INT)", "CREATE SCHEMA tx_wire_comment_schema", "CREATE ROLE tx_wire_comment_role", "CREATE SEQUENCE tx_wire_comment_sequence", "CREATE FOREIGN DATA WRAPPER tx_wire_comment_fdw", "CREATE SERVER tx_wire_comment_server FOREIGN DATA WRAPPER tx_wire_comment_fdw", "CREATE FOREIGN TABLE tx_wire_comment_foreign(a INT) SERVER tx_wire_comment_server"} {
				serverWantCode(t, w.query(t, q), "")
			}
			defer func() {
				serverWantCode(t, w.query(t, "ROLLBACK"), "")
				serverWantCode(t, w.query(t, "DROP SERVER tx_wire_comment_server CASCADE"), "")
				serverWantCode(t, w.query(t, "DROP FOREIGN DATA WRAPPER tx_wire_comment_fdw"), "")
			}()
			for i, k := range []struct{ target, probe string }{
				{"TABLE tx_wire_comment", "obj_description('tx_wire_comment'::regclass,'pg_class')"},
				{"COLUMN tx_wire_comment.a", "col_description('tx_wire_comment'::regclass,1)"},
				{"FOREIGN TABLE tx_wire_comment_foreign", "obj_description('tx_wire_comment_foreign'::regclass,'pg_class')"},
				{"COLUMN tx_wire_comment_foreign.a", "col_description('tx_wire_comment_foreign'::regclass,1)"},
				{"SCHEMA tx_wire_comment_schema", "obj_description('tx_wire_comment_schema'::regnamespace,'pg_namespace')"},
				{"ROLE tx_wire_comment_role", "(SELECT shobj_description(oid,'pg_authid') FROM pg_roles WHERE rolname='tx_wire_comment_role')"},
				{"SEQUENCE tx_wire_comment_sequence", "obj_description('tx_wire_comment_sequence'::regclass,'pg_class')"},
			} {
				q := "COMMENT ON " + k.target + " IS 'new'"
				probe := "SELECT " + k.probe
				serverWantCode(t, w.query(t, "COMMENT ON "+k.target+" IS 'old'"), "")
				name := fmt.Sprintf("comment_%d", i)
				frames := serverCycle(t, w, serverParse(name, q), serverDescribe('S', name))
				serverWantTags(t, frames, "1tnZ")
				serverWantReady(t, frames, 'I')
				serverScalar(t, peer, probe, "old")
				frames = serverCycle(t, w, serverBind(name+"_auto", name, nil, nil), serverExecute(name+"_auto", 0))
				serverWantTags(t, frames, "2CZ")
				serverWantReady(t, frames, 'I')
				transactionalCommentWireTag(t, frames)
				serverScalar(t, peer, probe, "new")
				serverWantCode(t, w.query(t, "COMMENT ON "+k.target+" IS 'old'"), "")
				for _, commit := range []bool{false, true} {
					serverWantCode(t, w.query(t, "BEGIN"), "")
					frames = serverCycle(t, w, serverDescribe('S', name))
					serverWantTags(t, frames, "tnZ")
					serverWantReady(t, frames, 'T')
					frames = serverCycle(t, w, serverBind(name+"_tx", name, nil, nil), serverDescribe('P', name+"_tx"))
					serverWantTags(t, frames, "2nZ")
					serverWantReady(t, frames, 'T')
					serverScalar(t, w, probe, "old")
					serverScalar(t, peer, probe, "old")
					frames = serverCycle(t, w, serverExecute(name+"_tx", 0))
					serverWantTags(t, frames, "CZ")
					serverWantReady(t, frames, 'T')
					transactionalCommentWireTag(t, frames)
					serverScalar(t, w, probe, "new")
					serverScalar(t, peer, probe, "old")
					serverWantCode(t, w.query(t, "SAVEPOINT mark"), "")
					serverWantCode(t, w.query(t, "COMMENT ON "+k.target+" IS 'later'"), "")
					serverNotificationError(t, w.query(t, "COMMENT ON COLUMN tx_wire_comment.missing IS NULL"), "42703", `column "missing" of relation "tx_wire_comment" does not exist`, "")
					frames = w.query(t, "SELECT 1")
					serverNotificationError(t, frames, "25P02", "current transaction is aborted, commands ignored until end of transaction block", "")
					serverWantReady(t, frames, 'E')
					serverWantCode(t, w.query(t, "ROLLBACK TO mark"), "")
					serverScalar(t, w, probe, "new")
					serverWantCode(t, w.query(t, "RELEASE mark"), "")
					ending, want := "ROLLBACK", "old"
					if commit {
						ending, want = "COMMIT", "new"
					}
					frames = w.query(t, ending)
					serverWantCode(t, frames, "")
					serverWantReady(t, frames, 'I')
					serverScalar(t, peer, probe, want)
				}
				serverWantCode(t, w.query(t, "COMMENT ON "+k.target+" IS 'old'"), "")
				failure := w.query(t, q+"; COMMENT ON COLUMN tx_wire_comment.missing IS NULL")
				serverWantCode(t, failure, "42703")
				serverWantReady(t, failure, 'I')
				serverScalar(t, peer, probe, "old")
				serverWantCode(t, w.query(t, "BEGIN; "+q+"; ROLLBACK"), "")
				serverScalar(t, peer, probe, "old")
				serverWantCode(t, w.query(t, q), "")
				serverScalar(t, peer, probe, "new")
			}
		})
	}
}

func transactionalCommentWireTag(t *testing.T, frames []serverFrame) {
	t.Helper()
	count := 0
	for _, frame := range frames {
		if frame.tag == 'C' {
			count++
			if string(frame.body) != "COMMENT\x00" {
				t.Fatalf("command tag %q", frame.body)
			}
		}
	}
	if count != 1 {
		t.Fatalf("COMMENT completions %d want 1", count)
	}
}
