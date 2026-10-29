package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Historical DROP OWNED is an owner-as-grantor REVOKE RESTRICT, not a sweep
// removing every occurrence of the selected principal from every snapshot.
func ipDropOwnedGrantorWitness(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	owner := ipOwner(t, p, b)
	reader := ipRole(t, p, b, "ip_reader")
	other := ipRole(t, p, b, "ip_other")
	ipExec(t, p, b, b.DB, "CREATE TABLE ip_restrict(a INT)", "GRANT SELECT ON ip_restrict TO ip_reader WITH GRANT OPTION")
	conn, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defer conn.ExecContext(p.Ctx, "RESET ROLE")
	ipExec(t, p, b, conn, "GRANT USAGE ON SCHEMA public TO ip_reader", "SET ROLE ip_reader", "GRANT SELECT ON ip_restrict TO ip_other", "RESET ROLE", "REVOKE USAGE ON SCHEMA public FROM ip_reader", "ALTER EXTENSION pageinspect ADD TABLE ip_restrict", "REVOKE SELECT ON ip_restrict FROM ip_reader CASCADE")
	oid := ipOID(t, p, b, "SELECT oid FROM pg_class WHERE relname='ip_restrict'")
	grants := []ipGrant{{owner, owner, ipTableRights, false}, {reader, owner, []string{"SELECT"}, true}, {other, reader, []string{"SELECT"}, false}}
	ipACL(t, p, b, b.DB, 1259, oid, 0, "e", grants...)
	denied := fixture.ImplicitError("DROP OWNED BY ip_reader", fixture.Diagnostic{Code: "2BP01", Message: "dependent privileges exist", Hint: "Use CASCADE to revoke them too."})
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, denied)
	ipACL(t, p, b, b.DB, 1259, oid, 0, "e", grants...)
	ipCheck(t, p, b, b.DB, "SELECT has_table_privilege('ip_reader','ip_restrict','SELECT') AS reader,has_table_privilege('ip_other','ip_restrict','SELECT') AS other", []string{"reader", "other"}, []string{"BOOL", "BOOL"}, [][]any{{false, false}})
	ipCheck(t, p, b, b.DB, "SELECT e.extname FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid=1259 AND d.objid=$1 AND d.deptype='e'", []string{"extname"}, []string{"NAME"}, [][]any{{"pageinspect"}}, oid)
	ipExec(t, p, b, b.DB, "ALTER EXTENSION pageinspect DROP TABLE ip_restrict", "DROP TABLE ip_restrict", "CREATE TABLE ip_mismatch(a INT)", "GRANT SELECT ON ip_mismatch TO ip_reader", "ALTER EXTENSION pageinspect ADD TABLE ip_mismatch", "ALTER TABLE ip_mismatch OWNER TO ip_other", "REVOKE SELECT ON ip_mismatch FROM ip_reader")
	mismatch := ipOID(t, p, b, "SELECT oid FROM pg_class WHERE relname='ip_mismatch'")
	ipTableACL(t, p, b, b.DB, mismatch, owner, reader, "SELECT")
	ipExec(t, p, b, b.DB, "DROP OWNED BY ip_reader")
	ipTableACL(t, p, b, b.DB, mismatch, owner, reader, "SELECT")
	blocked := fixture.ImplicitError("DROP ROLE ip_reader", fixture.Diagnostic{Code: "2BP01", Message: `role "ip_reader" cannot be dropped because some objects depend on it`, Detail: "initial privileges for table ip_mismatch"})
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, blocked)
	ipExec(t, p, b, b.DB, "REASSIGN OWNED BY ip_reader TO ip_other")
	ipTableACL(t, p, b, b.DB, mismatch, owner, other, "SELECT")
	ipExec(t, p, b, b.DB, "DROP ROLE ip_reader", "ALTER EXTENSION pageinspect DROP TABLE ip_mismatch", "DROP TABLE ip_mismatch")
	ipAbsent(t, p, b, b.DB, 1259, mismatch)
}
