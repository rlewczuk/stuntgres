package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func ipGrantorWitness(t *testing.T, p *fixture.Pair, b fixture.Backend, owner, reader int64) {
	t.Helper()
	other := ipRole(t, p, b, "ip_other")
	ipExec(t, p, b, b.DB, "CREATE TABLE ip_options(a INT)", "GRANT SELECT ON ip_options TO PUBLIC", "GRANT SELECT ON ip_options TO ip_reader WITH GRANT OPTION")
	conn, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defer conn.ExecContext(p.Ctx, "RESET ROLE")
	ipExec(t, p, b, conn, "GRANT USAGE ON SCHEMA public TO ip_reader", "SET ROLE ip_reader", "GRANT SELECT ON ip_options TO ip_other WITH GRANT OPTION", "RESET ROLE", "REVOKE USAGE ON SCHEMA public FROM ip_reader", "ALTER EXTENSION pageinspect ADD TABLE ip_options")
	oid := ipOID(t, p, b, "SELECT oid FROM pg_class WHERE relname='ip_options'")
	grants := []ipGrant{{owner, owner, ipTableRights, false}, {0, owner, []string{"SELECT"}, false}, {reader, owner, []string{"SELECT"}, true}, {other, reader, []string{"SELECT"}, true}}
	ipACL(t, p, b, b.DB, 1259, oid, 0, "e", grants...)
	ipExec(t, p, b, b.DB, "REVOKE SELECT ON ip_options FROM ip_reader CASCADE", "REVOKE SELECT ON ip_options FROM PUBLIC")
	ipACL(t, p, b, b.DB, 1259, oid, 0, "e", grants...)
	ipExec(t, p, b, b.DB, "ALTER EXTENSION pageinspect DROP TABLE ip_options", "DROP TABLE ip_options")
	ipAbsent(t, p, b, b.DB, 1259, oid)
	ipExec(t, p, b, b.DB, "CREATE FUNCTION ip_empty() RETURNS integer LANGUAGE SQL AS 'SELECT 1'", "REVOKE ALL ON FUNCTION ip_empty() FROM PUBLIC,CURRENT_USER", "ALTER EXTENSION pageinspect ADD FUNCTION ip_empty()")
	empty := ipOID(t, p, b, "SELECT oid FROM pg_proc WHERE proname='ip_empty'")
	ipAbsent(t, p, b, b.DB, 1255, empty)
	ipExec(t, p, b, b.DB, "CREATE TABLE ip_null(a INT)", "ALTER EXTENSION pageinspect ADD TABLE ip_null")
	ipAbsent(t, p, b, b.DB, 1259, ipOID(t, p, b, "SELECT oid FROM pg_class WHERE relname='ip_null'"))
	ipAbsent(t, p, b, b.DB, 1247, ipOID(t, p, b, "SELECT oid FROM pg_type WHERE typname='ip_null' AND typnamespace='public'::regnamespace"))
	ipExec(t, p, b, b.DB, "CREATE TABLE ip_cascade(a INT,b INT)", "GRANT SELECT ON ip_cascade TO ip_reader", "GRANT UPDATE(b) ON ip_cascade TO ip_reader", "ALTER EXTENSION pageinspect ADD TABLE ip_cascade")
	cascade := ipOID(t, p, b, "SELECT oid FROM pg_class WHERE relname='ip_cascade'")
	ipTableACL(t, p, b, b.DB, cascade, owner, reader, "SELECT")
	ipACL(t, p, b, b.DB, 1259, cascade, 2, "e", ipGrant{reader, owner, []string{"UPDATE"}, false})
	ipExec(t, p, b, b.DB, "DROP EXTENSION pageinspect CASCADE")
	ipAbsent(t, p, b, b.DB, 1255, empty)
	ipAbsent(t, p, b, b.DB, 1259, cascade)
	ipExec(t, p, b, b.DB, "CREATE EXTENSION pageinspect")
}

func ipHistoricalOwnerWitness(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	ipExec(t, p, b, b.DB, "CREATE ROLE ip_owner", "CREATE ROLE ip_target", "CREATE TABLE ip_old_owner(a INT)", "ALTER TABLE ip_old_owner OWNER TO ip_owner")
	conn, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defer conn.ExecContext(p.Ctx, "RESET ROLE")
	ipExec(t, p, b, conn, "GRANT USAGE ON SCHEMA public TO ip_owner", "SET ROLE ip_owner", "GRANT SELECT ON ip_old_owner TO ip_reader", "RESET ROLE", "REVOKE USAGE ON SCHEMA public FROM ip_owner", "ALTER EXTENSION pageinspect ADD TABLE ip_old_owner", "ALTER TABLE ip_old_owner OWNER TO CURRENT_USER")
	oid := ipOID(t, p, b, "SELECT oid FROM pg_class WHERE relname='ip_old_owner'")
	owner := ipRole(t, p, b, "ip_owner")
	reader := ipRole(t, p, b, "ip_reader")
	target := ipRole(t, p, b, "ip_target")
	ipTableACL(t, p, b, b.DB, oid, owner, reader, "SELECT")
	ipExec(t, p, b, b.DB, "REASSIGN OWNED BY ip_owner TO ip_target")
	ipTableACL(t, p, b, b.DB, oid, target, reader, "SELECT")
	ipExec(t, p, b, b.DB, "DROP ROLE ip_owner", "ALTER EXTENSION pageinspect DROP TABLE ip_old_owner", "DROP TABLE ip_old_owner", "DROP ROLE ip_target")
}
