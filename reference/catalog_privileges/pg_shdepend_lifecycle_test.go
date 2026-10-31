package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGShdependLifecycle(t *testing.T) {
	p := shScenario(t)
	for _, b := range p.Backends() {
		db, table, owner, reader, grantor := shSetup(t, p, b)
		q := "SELECT * FROM pg_catalog.pg_shdepend WHERE dbid=$1 AND classid=1259 AND objid=$2"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		check := func(expected ...[]any) {
			t.Helper()
			fixture.PgDescriptionExecutorRows(t, p, b, held, shSelect(q, expected, db, table))
		}
		o := shEdge(db, 1259, table, 0, owner, "o")
		a := shEdge(db, 1259, table, 0, reader, "a")
		check(o)
		old, err := stmt.QueryContext(p.Ctx, db, table)
		if err != nil {
			t.Fatal(err)
		}
		shExec(t, p, b, b.DB, "GRANT SELECT ON sh_table TO sh_reader")
		check(o, a)
		tx, err := b.DB.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		shExec(t, p, b, tx, "SAVEPOINT sh_save", "GRANT UPDATE(b) ON sh_table TO sh_grantor", "CREATE POLICY sh_private ON sh_table TO sh_grantor USING (true)", "ALTER DEFAULT PRIVILEGES FOR ROLE sh_owner GRANT SELECT ON TABLES TO sh_grantor", "ALTER TABLE sh_table OWNER TO sh_reader")
		shBag(t, p, b, tx, 1259, table, shEdge(db, 1259, table, 0, reader, "o"), shEdge(db, 1259, table, 2, grantor, "a"))
		privatePolicy := shOID(t, p, tx, "SELECT oid FROM pg_policy WHERE polname='sh_private'")
		privateDefault := shOID(t, p, tx, "SELECT oid FROM pg_default_acl WHERE defaclrole=$1", owner)
		shBag(t, p, b, tx, 3256, privatePolicy, shEdge(db, 3256, privatePolicy, 0, grantor, "r"))
		shBag(t, p, b, tx, 826, privateDefault, shEdge(db, 826, privateDefault, 0, owner, "o"), shEdge(db, 826, privateDefault, 0, grantor, "a"))
		shExec(t, p, b, tx, "ROLLBACK TO sh_save")
		shBag(t, p, b, tx, 1259, table, o, a)
		shBag(t, p, b, tx, 3256, privatePolicy)
		shBag(t, p, b, tx, 826, privateDefault)
		shExec(t, p, b, tx, "REVOKE SELECT ON sh_table FROM sh_reader")
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		check(o, a)
		committed, err := b.DB.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		shExec(t, p, b, committed, "REVOKE SELECT ON sh_table FROM sh_reader")
		if err := committed.Commit(); err != nil {
			t.Fatal(err)
		}
		check(o)
		shExec(t, p, b, b.DB, "GRANT SELECT ON sh_table TO sh_reader")
		check(o, a)
		dynamicQ := "SELECT * FROM pg_catalog.pg_shdepend WHERE classid=$1 AND objid=$2"
		dynamicStmt, err := b.DB.PrepareContext(p.Ctx, dynamicQ)
		if err != nil {
			t.Fatal(err)
		}
		dynamicHeld := fixture.HeldStatement{Executor: b.DB, Statement: dynamicStmt, SQL: dynamicQ}
		dynamicCheck := func(class, object int64, expected ...[]any) {
			t.Helper()
			fixture.PgDescriptionExecutorRows(t, p, b, dynamicHeld, shSelect(dynamicQ, expected, class, object))
		}
		shExec(t, p, b, b.DB, "CREATE POLICY sh_living ON sh_table TO sh_reader USING (true)", "ALTER DEFAULT PRIVILEGES FOR ROLE sh_owner GRANT SELECT ON TABLES TO sh_reader")
		policy := shOID(t, p, b.DB, "SELECT oid FROM pg_policy WHERE polname='sh_living'")
		def := shOID(t, p, b.DB, "SELECT oid FROM pg_default_acl WHERE defaclrole=$1", owner)
		dynamicCheck(3256, policy, shEdge(db, 3256, policy, 0, reader, "r"))
		dynamicCheck(826, def, shEdge(db, 826, def, 0, owner, "o"), shEdge(db, 826, def, 0, reader, "a"))
		shExec(t, p, b, b.DB, "ALTER POLICY sh_living ON sh_table TO sh_grantor")
		dynamicCheck(3256, policy, shEdge(db, 3256, policy, 0, grantor, "r"))
		shExec(t, p, b, b.DB, "DROP POLICY sh_living ON sh_table", "ALTER DEFAULT PRIVILEGES FOR ROLE sh_owner REVOKE SELECT ON TABLES FROM sh_reader")
		dynamicCheck(3256, policy)
		dynamicCheck(826, def)
		dynamicStmt.Close()
		s := fixture.ImplicitError("GRANT SELECT ON sh_table TO sh_reader,sh_missing", fixture.Diagnostic{Code: "42704", Message: "role \"sh_missing\" does not exist"})
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		check(o, a)
		shExec(t, p, b, b.DB, "ALTER ROLE sh_reader RENAME TO sh_renamed")
		check(o, a)
		shExec(t, p, b, b.DB, "ALTER ROLE sh_renamed RENAME TO sh_reader", "ALTER TABLE sh_table OWNER TO sh_reader")
		check(shEdge(db, 1259, table, 0, reader, "o"))
		shExec(t, p, b, b.DB, "DROP TABLE sh_table")
		check()
		stmt.Close()
		shRetained(t, old, [][]any{o})
		shExec(t, p, b, b.DB, "CREATE TABLE sh_drop(a INT)", "ALTER TABLE sh_drop OWNER TO sh_owner")
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.ImplicitError("DROP ROLE sh_owner", fixture.Diagnostic{Code: "2BP01", Message: "role \"sh_owner\" cannot be dropped because some objects depend on it", Detail: "owner of table sh_drop"}))
		shExec(t, p, b, b.DB, "DROP TABLE sh_drop", "DROP ROLE sh_owner")
	}
}
