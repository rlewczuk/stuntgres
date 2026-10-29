package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGInitPrivsTransactions(t *testing.T) {
	ipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		oid, owner, reader := ipMemberSetup(t, p, b)
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer conn.ExecContext(p.Ctx, "ROLLBACK")
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		q := "SELECT objoid,classoid,objsubid,privtype FROM pg_init_privs WHERE classoid=1259 AND objoid=$1"
		stmt, err := peer.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: peer, Statement: stmt, SQL: q}
		readHeld := func(want [][]any) {
			s := fixture.Select(q, ipColumns[:4], ipTypes[:4], want, oid)
			s.Bag = true
			fixture.PGDatabaseCheck(t, p, b, held, s)
		}
		ipExec(t, p, b, conn, "BEGIN", "ALTER EXTENSION pageinspect ADD TABLE ip_member")
		ipTableACL(t, p, b, conn, oid, owner, reader, "SELECT")
		ipAbsent(t, p, b, peer, 1259, oid)
		readHeld(nil)
		ipExec(t, p, b, conn, "SAVEPOINT ip_save", "ALTER EXTENSION pageinspect DROP TABLE ip_member")
		ipAbsent(t, p, b, conn, 1259, oid)
		ipExec(t, p, b, conn, "GRANT INSERT ON ip_member TO ip_reader", "ALTER EXTENSION pageinspect ADD TABLE ip_member")
		ipACL(t, p, b, conn, 1259, oid, 0, "e", ipGrant{owner, owner, ipTableRights, false}, ipGrant{reader, owner, []string{"SELECT", "INSERT"}, false})
		ipExec(t, p, b, conn, "ROLLBACK TO SAVEPOINT ip_save")
		ipTableACL(t, p, b, conn, oid, owner, reader, "SELECT")
		ipExec(t, p, b, conn, "COMMIT")
		readHeld([][]any{{oid, int64(1259), int64(0), "e"}, {oid, int64(1259), int64(2), "e"}})
		ipTableACL(t, p, b, peer, oid, owner, reader, "SELECT")
		for _, tc := range []struct{ q, message string }{{"ALTER EXTENSION pageinspect ADD TABLE ip_member", `table ip_member is already a member of extension "pageinspect"`}} {
			s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "55000", Message: tc.message})
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
			ipTableACL(t, p, b, peer, oid, owner, reader, "SELECT")
		}
		ipExec(t, p, b, conn, "BEGIN", "ALTER EXTENSION pageinspect DROP TABLE ip_member")
		ipAbsent(t, p, b, conn, 1259, oid)
		ipTableACL(t, p, b, peer, oid, owner, reader, "SELECT")
		ipExec(t, p, b, conn, "ROLLBACK")
		ipTableACL(t, p, b, peer, oid, owner, reader, "SELECT")
		ipExec(t, p, b, conn, "ALTER EXTENSION pageinspect DROP TABLE ip_member")
		s := fixture.ImplicitError("ALTER EXTENSION pageinspect DROP TABLE ip_member", fixture.Diagnostic{Code: "55000", Message: `table ip_member is not a member of extension "pageinspect"`})
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
		readHeld(nil)
	})
}

func TestReferencePGInitPrivsRoles(t *testing.T) {
	ipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		oid, owner, reader := ipMemberSetup(t, p, b)
		ipExec(t, p, b, b.DB, "CREATE ROLE ip_target", "ALTER EXTENSION pageinspect ADD TABLE ip_member", "REVOKE ALL ON ip_member FROM ip_reader", "REVOKE ALL(a,b) ON ip_member FROM ip_reader")
		ipTableACL(t, p, b, b.DB, oid, owner, reader, "SELECT")
		retained := ipRetainRoleRows(t, p, b, oid)
		s := fixture.ImplicitError("DROP ROLE ip_reader", fixture.Diagnostic{Code: "2BP01", Message: `role "ip_reader" cannot be dropped because some objects depend on it`, Detail: "initial privileges for table ip_member\ninitial privileges for column b of table ip_member"})
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		ipExec(t, p, b, b.DB, "ALTER ROLE ip_reader RENAME TO ip_renamed")
		ipTableACL(t, p, b, b.DB, oid, owner, reader, "SELECT")
		ipCheck(t, p, b, b.DB, "SELECT initprivs::text LIKE '%ip_renamed=%' AS renamed FROM pg_init_privs WHERE classoid=1259 AND objoid=$1 AND objsubid=0", []string{"renamed"}, []string{"BOOL"}, [][]any{{true}}, oid)
		target := ipRole(t, p, b, "ip_target")
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer conn.ExecContext(p.Ctx, "ROLLBACK")
		ipExec(t, p, b, conn, "BEGIN", "REASSIGN OWNED BY ip_renamed TO ip_target")
		ipTableACL(t, p, b, conn, oid, owner, target, "SELECT")
		ipExec(t, p, b, conn, "ROLLBACK")
		ipTableACL(t, p, b, b.DB, oid, owner, reader, "SELECT")
		ipExec(t, p, b, conn, "BEGIN", "REASSIGN OWNED BY ip_renamed TO ip_target", "COMMIT")
		ipTableACL(t, p, b, b.DB, oid, owner, target, "SELECT")
		ipACL(t, p, b, b.DB, 1259, oid, 2, "e", ipGrant{target, owner, []string{"UPDATE"}, false})
		ipExec(t, p, b, b.DB, "DROP ROLE ip_renamed", "CREATE ROLE ip_reader")
		ipTableACL(t, p, b, b.DB, oid, owner, target, "SELECT")
		retained()
		ipExec(t, p, b, b.DB, "DROP OWNED BY ip_target")
		ipACL(t, p, b, b.DB, 1259, oid, 0, "e", ipGrant{owner, owner, ipTableRights, false})
		ipCheck(t, p, b, b.DB, "SELECT objsubid FROM pg_init_privs WHERE classoid=1259 AND objoid=$1 AND objsubid>0", []string{"objsubid"}, []string{"INT4"}, nil, oid)
		ipExec(t, p, b, b.DB, "DROP ROLE ip_target")
		ipDropOwnedGrantorWitness(t, p, b)
	})
}
