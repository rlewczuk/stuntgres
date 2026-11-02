package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgsurgeryFreezePermissions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"fs_owner", "fs_reader"})
	freezeCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			"CREATE ROLE fs_owner", "CREATE ROLE fs_reader", "GRANT USAGE ON SCHEMA fs_part TO fs_owner,fs_reader",
			"ALTER TABLE fs_part.heap OWNER TO fs_owner", "GRANT SELECT,UPDATE ON fs_part.heap TO fs_reader",
			"CREATE TABLE fs_part.inputs(n regclass)", "GRANT SELECT ON fs_part.inputs TO fs_owner,fs_reader",
		} {
			freezeRun(t, p, b, c, fixture.Exec(q, 0))
		}
		freezeRun(t, p, b, c, fixture.Exec("INSERT INTO fs_part.inputs VALUES(NULL)", 1))
		a := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(1,'A') RETURNING k,v,ctid,xmin,xmax")
		q := "SELECT fs_part.heap_force_freeze(COALESCE($1::regclass,i.n),$2::tid[]) AS repaired FROM fs_part.inputs i"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		denied := func(target any, message string) {
			s := freezeError(q, "42501", message, target, freezeTIDs(a.tid))
			freezeBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		for _, role := range []string{"fs_owner", "fs_reader"} {
			freezeRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			denied(nil, "permission denied for function heap_force_freeze")
			denied("fs_part.heap", "permission denied for function heap_force_freeze")
			freezeRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		freezeRun(t, p, b, c, fixture.Exec("GRANT EXECUTE ON FUNCTION fs_part.heap_force_freeze(regclass,tid[]) TO fs_reader", 0))
		freezeRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION fs_reader", 0))
		denied("fs_part.heap", "must be owner of table heap")
		// NULL still avoids the body after routine admission succeeds.
		freezeBoth(t, p, b, c, fixture.Select(q, []string{"repaired"}, []string{"VOID"}, [][]any{{nil}}, nil, freezeTIDs(a.tid)))
		freezeRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		freezeExpect(t, p, b, c, a)
		freezeRun(t, p, b, c, fixture.Exec("GRANT EXECUTE ON FUNCTION fs_part.heap_force_freeze(regclass,tid[]) TO fs_owner", 0))
		freezeRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION fs_owner", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, freezeVoid(q, "fs_part.heap", freezeTIDs(a.tid)))
		freezeRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		freezeExpect(t, p, b, c, freezeRepaired(a))
		freezeRun(t, p, b, c, fixture.Exec("REVOKE EXECUTE ON FUNCTION fs_part.heap_force_freeze(regclass,tid[]) FROM fs_owner", 0))
		freezeRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION fs_owner", 0))
		denied("fs_part.heap", "permission denied for function heap_force_freeze")
		freezeRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		for _, ddl := range []string{"CREATE TABLE fs_part.divisor(d integer)", "GRANT SELECT ON fs_part.divisor TO fs_owner"} {
			freezeRun(t, p, b, c, fixture.Exec(ddl, 0))
		}
		freezeRun(t, p, b, c, fixture.Exec("INSERT INTO fs_part.divisor VALUES(0)", 1))
		child := "SELECT fs_part.heap_force_freeze('fs_part.heap'::regclass,ARRAY[('(0,'||(1/d)::text||')')::tid]) AS repaired FROM fs_part.divisor"
		childStmt, err := c.PrepareContext(p.Ctx, child)
		if err != nil {
			t.Fatal(err)
		}
		defer childStmt.Close()
		childHeld := fixture.HeldStatement{Executor: c, Statement: childStmt, SQL: child}
		freezeRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION fs_owner", 0))
		childDenied := freezeError(child, "42501", "permission denied for function heap_force_freeze")
		freezeBoth(t, p, b, c, childDenied)
		fixture.RunStep(t, p.Ctx, childHeld, b.Recorder, childDenied)
		freezeRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		freezeRun(t, p, b, c, fixture.Exec("GRANT EXECUTE ON FUNCTION fs_part.heap_force_freeze(regclass,tid[]) TO fs_owner", 0))
		freezeRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION fs_owner", 0))
		childError := freezeError(child, "22012", "division by zero")
		freezeBoth(t, p, b, c, childError)
		fixture.RunStep(t, p.Ctx, childHeld, b.Recorder, childError)
		freezeRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		freezeExpect(t, p, b, c, freezeRepaired(a))
		freezeRun(t, p, b, c, fixture.Exec("REVOKE USAGE ON SCHEMA fs_part FROM fs_owner", 0))
		freezeRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION fs_owner", 0))
		schemaDenied := freezeError(freezeSQL, "42501", "permission denied for schema fs_part", "fs_part.heap", "{}")
		schemaDenied.Diagnostic.Position = "8"
		freezeBoth(t, p, b, c, schemaDenied)
		freezeRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		freezeRun(t, p, b, c, fixture.Exec("DROP EXTENSION pg_surgery", 0))
		freezeRun(t, p, b, c, fixture.Exec("GRANT CREATE ON SCHEMA fs_part TO fs_reader", 0))
		freezeRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION fs_reader", 0))
		s := freezeError("CREATE EXTENSION pg_surgery SCHEMA fs_part", "42501", "permission denied to create extension \"pg_surgery\"")
		s.SelectRows = false
		s.Diagnostic.Hint = "Must be superuser to create this extension."
		freezeRun(t, p, b, c, s)
		freezeRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		freezeBoth(t, p, b, c, fixture.Select("SELECT extname FROM pg_extension WHERE extname='pg_surgery'", []string{"extname"}, []string{"NAME"}, nil))
	})
}

func TestReferencePgsurgeryFreezeIdentity(t *testing.T) {
	freezeCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		a := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(1,'A') RETURNING k,v,ctid,xmin,xmax")
		var oid int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'fs_part.heap_force_freeze(regclass,tid[])'::regprocedure::oid").Scan(&oid); err != nil || oid <= 0 {
			t.Fatal("installed identity", oid, err)
		}
		st, err := c.PrepareContext(p.Ctx, freezeSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		for _, q := range []string{"CREATE SCHEMA fs_moved", "ALTER FUNCTION fs_part.heap_force_freeze(regclass,tid[]) RENAME TO repair", "ALTER FUNCTION fs_part.repair(regclass,tid[]) SET SCHEMA fs_moved"} {
			freezeRun(t, p, b, c, fixture.Exec(q, 0))
		}
		freezeBoth(t, p, b, c, fixture.Select("SELECT 'fs_moved.repair(regclass,tid[])'::regprocedure::oid::bigint AS identity", []string{"identity"}, []string{"INT8"}, [][]any{{oid}}))
		// Ordinary same-name SQL code cannot inherit the installed capability.
		freezeRun(t, p, b, c, fixture.Exec("CREATE FUNCTION fs_part.heap_force_freeze(regclass,tid[]) RETURNS void LANGUAGE SQL AS 'SELECT NULL::void'", 0))
		freezeBoth(t, p, b, c, fixture.Select(freezeSQL, []string{"repaired"}, []string{"VOID"}, [][]any{{nil}}, "fs_part.heap", freezeTIDs(a.tid)))
		freezeExpect(t, p, b, c, a)
		q := "SELECT fs_moved.repair($1::regclass,$2::tid[]) AS repaired"
		freezeBoth(t, p, b, c, freezeVoid(q, "fs_part.heap", freezeTIDs(a.tid)))
		freezeExpect(t, p, b, c, freezeRepaired(a))
		for _, ddl := range []string{"ALTER FUNCTION fs_moved.repair(regclass,tid[]) SET SCHEMA fs_part", "ALTER EXTENSION pg_surgery SET SCHEMA fs_moved"} {
			freezeRun(t, p, b, c, fixture.Exec(ddl, 0))
		}
		freezeBoth(t, p, b, c, fixture.Select("SELECT 'fs_moved.repair(regclass,tid[])'::regprocedure::oid::bigint AS identity", []string{"identity"}, []string{"INT8"}, [][]any{{oid}}))
		freezeRun(t, p, b, c, fixture.Exec("ALTER EXTENSION pg_surgery DROP FUNCTION fs_moved.repair(regclass,tid[])", 0))
		freezeRun(t, p, b, c, fixture.Exec("DROP EXTENSION pg_surgery", 0))
		d := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(2,'detached') RETURNING k,v,ctid,xmin,xmax")
		freezeBoth(t, p, b, c, freezeVoid(q, "fs_part.heap", freezeTIDs(d.tid)))
		freezeExpect(t, p, b, c, freezeRepaired(a), freezeRepaired(d))
		// PostgreSQL may reparse a prepared SQL statement after name DDL. Assert
		// its current ordinary shadow, not fictional retained function binding.
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: freezeSQL}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(freezeSQL, []string{"repaired"}, []string{"VOID"}, [][]any{{nil}}, "fs_part.heap", freezeTIDs(d.tid)))
	})
}
