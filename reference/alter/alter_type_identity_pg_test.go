//go:build reference

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterTypeACLAndOwnedSequenceIdentity(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"alter_acl_reader", "alter_acl_outsider"})
	p := fixture.NewPair(t)
	c, err := p.Postgres.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	defer c.ExecContext(p.Ctx, "RESET ROLE")
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, p.PostgresRecord, s) }
	for _, q := range []string{"CREATE USER alter_acl_reader", "CREATE USER alter_acl_outsider", "GRANT USAGE ON SCHEMA public TO alter_acl_reader", "GRANT USAGE ON SCHEMA public TO alter_acl_outsider", "CREATE TABLE t(c INT)", "CREATE SEQUENCE q START 10 CACHE 7 OWNED BY t.c", "GRANT SELECT ON t TO alter_acl_reader", "GRANT UPDATE(c) ON t TO alter_acl_reader"} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
	query := "SELECT t.oid::bigint,t.relowner::bigint,t.relacl::text,a.attnum::bigint,a.attacl::text,q.oid::bigint,d.refobjid::bigint,d.refobjsubid::bigint FROM pg_class t JOIN pg_attribute a ON a.attrelid=t.oid AND a.attname='c' JOIN pg_class q ON q.oid='q'::regclass JOIN pg_depend d ON d.objid=q.oid AND d.deptype='a' WHERE t.oid='t'::regclass"
	var table, owner, attribute, sequence, ownedTable, ownedAttribute int64
	var tableACL, columnACL string
	if err = c.QueryRowContext(p.Ctx, query).Scan(&table, &owner, &tableACL, &attribute, &columnACL, &sequence, &ownedTable, &ownedAttribute); err != nil {
		t.Fatal(err)
	}
	if table <= 0 || owner <= 0 || attribute != 1 || sequence <= 0 || table == sequence || ownedTable != table || ownedAttribute != attribute || tableACL == "" || columnACL == "" {
		t.Fatal(table, owner, attribute, sequence, ownedTable, ownedAttribute, tableACL, columnACL)
	}
	run(fixture.Select("SELECT nextval('q')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(10)}}))
	run(fixture.Exec("ALTER TABLE t ALTER c TYPE BIGINT USING c+1", 0))
	var nextTable, nextOwner, nextAttribute, nextSequence, nextOwnedTable, nextOwnedAttribute int64
	var nextTableACL, nextColumnACL string
	if err = c.QueryRowContext(p.Ctx, query).Scan(&nextTable, &nextOwner, &nextTableACL, &nextAttribute, &nextColumnACL, &nextSequence, &nextOwnedTable, &nextOwnedAttribute); err != nil {
		t.Fatal(err)
	}
	if nextTable != table || nextOwner != owner || nextAttribute != attribute || nextSequence != sequence || nextOwnedTable != ownedTable || nextOwnedAttribute != ownedAttribute || nextTableACL != tableACL || nextColumnACL != columnACL {
		t.Fatal("TYPE changed identity/ACL/sequence ownership")
	}
	run(fixture.Select("SELECT nextval('q')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(11)}}))
	run(fixture.Exec("SET ROLE alter_acl_reader", 0))
	run(fixture.Select("SELECT c FROM t", []string{"c"}, []string{"INT8"}, [][]any{{int64(8)}}))
	run(fixture.Exec("UPDATE t SET c=9", 1))
	run(fixture.Select("SELECT c FROM t", []string{"c"}, []string{"INT8"}, [][]any{{int64(9)}}))
	run(fixture.Exec("RESET ROLE", 0))
	run(fixture.Exec("SET ROLE alter_acl_outsider", 0))
	failure := fixture.ImplicitError("SELECT c FROM t", fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"})
	failure.SelectRows, failure.ErrorStage = true, "execute"
	run(failure)
	run(fixture.Exec("RESET ROLE", 0))
}
