package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGInitPrivsSmoke(t *testing.T) {
	ipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, ipShapeStep("SELECT * FROM pg_init_privs WHERE FALSE", b.PG))
		owner := ipOwner(t, p, b)
		ipACL(t, p, b, b.DB, 1259, 3394, 0, "i", ipGrant{owner, owner, ipTableRights, false}, ipGrant{0, owner, []string{"SELECT"}, false})
		oid, owner, reader := ipMemberSetup(t, p, b)
		ipAbsent(t, p, b, b.DB, 1259, oid)
		ipExec(t, p, b, b.DB, "ALTER EXTENSION pageinspect ADD TABLE ip_member")
		ipTableACL(t, p, b, b.DB, oid, owner, reader, "SELECT")
		ipExec(t, p, b, b.DB, "REVOKE SELECT ON ip_member FROM ip_reader", "GRANT INSERT ON ip_member TO ip_reader")
		ipTableACL(t, p, b, b.DB, oid, owner, reader, "SELECT")
		ipCheck(t, p, b, b.DB, "SELECT has_table_privilege('ip_reader','ip_member','SELECT') AS readable,has_table_privilege('ip_reader','ip_member','INSERT') AS writable", []string{"readable", "writable"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}})
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer conn.ExecContext(p.Ctx, "RESET ROLE")
		defer func() {
			for _, q := range []string{"RESET ROLE", "GRANT SELECT ON pg_catalog.pg_init_privs TO PUBLIC"} {
				if _, err := conn.ExecContext(p.Ctx, q); err != nil {
					t.Error(err)
				}
			}
		}()
		ipExec(t, p, b, conn, "GRANT USAGE ON SCHEMA public TO ip_other", "SET ROLE ip_other")
		ipTableACL(t, p, b, conn, oid, owner, reader, "SELECT")
		s := fixture.ImplicitError("SELECT * FROM ip_member", fixture.Diagnostic{Code: "42501", Message: "permission denied for table ip_member"})
		s.SelectRows = true
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
		ipExec(t, p, b, conn, "RESET ROLE", "REVOKE USAGE ON SCHEMA public FROM ip_other", "REVOKE SELECT ON pg_catalog.pg_init_privs FROM PUBLIC", "SET ROLE ip_other")
		denied := fixture.ImplicitError("SELECT * FROM pg_init_privs WHERE FALSE", fixture.Diagnostic{Code: "42501", Message: "permission denied for table pg_init_privs"})
		denied.SelectRows = true
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, denied)
		ipExec(t, p, b, conn, "RESET ROLE", "GRANT SELECT ON pg_catalog.pg_init_privs TO PUBLIC")
		ipExec(t, p, b, b.DB, "ALTER EXTENSION pageinspect DROP TABLE ip_member")
		ipAbsent(t, p, b, b.DB, 1259, oid)
		ipExec(t, p, b, b.DB, "ALTER EXTENSION pageinspect ADD TABLE ip_member")
		ipTableACL(t, p, b, b.DB, oid, owner, reader, "INSERT")
		if !b.PG {
			ipControlRoundTrip(t, p, b, oid, owner, reader, "INSERT")
		}
	})
}
