package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRoutineAlterSchemaBoundaries(t *testing.T) {
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c, "CREATE FUNCTION ra_boundary() RETURNS integer RETURN 5", "CREATE TEMP TABLE ra_temp(v integer)")
		routineAlterError(t, p, b, c, "ALTER FUNCTION ra_boundary() SET SCHEMA pg_temp", "0A000", "cannot move objects into or out of temporary schemas")
		routineAlterError(t, p, b, c, "ALTER FUNCTION ra_boundary() SET SCHEMA pg_toast", "0A000", "cannot move objects into or out of TOAST schema")
		routineValue(t, p, b, c, "SELECT ra_boundary() AS v", "v", "INT4", int64(5))
	})
}

func TestReferenceRoutineAlterOwnerOperands(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ra_operand", "CURRENT_USER", "CURRENT_ROLE", "SESSION_USER"})
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c, "CREATE ROLE ra_operand", `CREATE ROLE "CURRENT_USER"`, `CREATE ROLE "CURRENT_ROLE"`, `CREATE ROLE "SESSION_USER"`, "GRANT USAGE,CREATE ON SCHEMA public TO ra_operand", `GRANT USAGE,CREATE ON SCHEMA public TO "CURRENT_USER","CURRENT_ROLE","SESSION_USER"`, "CREATE FUNCTION ra_operand() RETURNS integer RETURN 5")
		for _, role := range []string{"CURRENT_USER", "CURRENT_ROLE", "SESSION_USER"} {
			routineExec(t, p, b, c, `ALTER FUNCTION ra_operand() OWNER TO "`+role+`"`)
			routineAlterRows(t, p, b, c, "SELECT proowner='\""+role+"\"'::regrole AS owned FROM pg_proc WHERE proname='ra_operand'", []string{"owned"}, []string{"BOOL"}, [][]any{{true}})
		}
		routineExec(t, p, b, c, "ALTER FUNCTION ra_operand() OWNER TO SESSION_USER", "SET ROLE ra_operand")
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		// Authenticated superuser differs from current role: CURRENT_USER is
		// not a literal role name and is not interchangeable with SESSION_USER.
		routineExec(t, p, b, c, "RESET ROLE", "ALTER FUNCTION ra_operand() OWNER TO ra_operand", "SET ROLE ra_operand", "ALTER FUNCTION ra_operand() OWNER TO CURRENT_USER", "ALTER FUNCTION ra_operand() OWNER TO CURRENT_ROLE")
		routineAlterRows(t, p, b, c, "SELECT proowner='ra_operand'::regrole AS owned FROM pg_proc WHERE proname='ra_operand'", []string{"owned"}, []string{"BOOL"}, [][]any{{true}})
		routineExec(t, p, b, c, "RESET ROLE")
	})
}

func TestReferenceRoutineAlterExplicitACLPersistenceBoundary(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ra_dump_reader"})
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c, "CREATE ROLE ra_dump_reader", "CREATE FUNCTION ra_acl_dump(integer) RETURNS integer RETURN $1+1", "GRANT EXECUTE ON FUNCTION ra_acl_dump(integer) TO ra_dump_reader", "ALTER FUNCTION ra_acl_dump(integer) RENAME TO ra_acl_dump_new")
		routineValue(t, p, b, c, "SELECT ra_acl_dump_new(4) AS v", "v", "INT4", int64(5))
		var owner string
		if err:=c.QueryRowContext(p.Ctx,"SELECT current_user").Scan(&owner);err!=nil {t.Fatal(err)}
		checkACL:=func(){
			routineValueWithNativeNullability(t,p,b,c,"SELECT has_function_privilege('ra_dump_reader','ra_acl_dump_new(integer)','EXECUTE') AS v","v","BOOL",true,true,true)
			s:=fixture.Select("SELECT unnest(pg_get_acl(1255,oid,0))::text AS acl FROM pg_proc WHERE proname='ra_acl_dump_new'",[]string{"acl"},[]string{"TEXT"},[][]any{{"=X/"+owner},{owner+"=X/"+owner},{"ra_dump_reader=X/"+owner}})
			s.Bag=true
			fixture.RunStep(t,p.Ctx,c,b.Recorder,s)
		}
		checkACL()
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		checkACL()
		routineValue(t, p, b, c, "SELECT ra_acl_dump_new(4) AS v", "v", "INT4", int64(5))
	})
}
