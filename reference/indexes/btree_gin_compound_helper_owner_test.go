package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Explicit ACLs precede the dependent index, isolating the lawful owner ACL
// projection rather than an unrelated grant/revoke admission path.
func TestReferenceBtreeGINCompoundHelperOwnerACL(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"compound_acl_reader", "compound_acl_denied", "compound_acl_owner"})
	fixture.SchemaReferenceCleanup(t, "compound_acl_schema")
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		compoundCleanup(t, p, b, conn)
		// The existing fixture installs as postgres natively and stuntgres on
		// pinned PostgreSQL; neither expected ACL is learned from native output.
		installer := "postgres"
		if b.PG {
			installer = "stuntgres"
		}
		for _, q := range []string{
			"CREATE ROLE compound_acl_owner", "CREATE ROLE compound_acl_reader", "CREATE ROLE compound_acl_denied",
			"CREATE SCHEMA compound_acl_schema",
			"GRANT USAGE,CREATE ON SCHEMA compound_acl_schema TO compound_acl_owner",
			"GRANT USAGE ON SCHEMA compound_acl_schema TO compound_acl_reader,compound_acl_denied",
			"CREATE EXTENSION btree_gin WITH SCHEMA compound_acl_schema VERSION '1.3'",
			"CREATE TYPE compound_acl_schema.compound_acl_enum AS ENUM('z','a','m')",
			"GRANT USAGE ON TYPE compound_acl_schema.compound_acl_enum TO compound_acl_owner,compound_acl_reader,compound_acl_denied",
			"CREATE TABLE compound_acl_schema.compound_acl_rows(id integer,v compound_acl_schema.compound_acl_enum)",
			"GRANT SELECT,INSERT,UPDATE,DELETE ON TABLE compound_acl_schema.compound_acl_rows TO compound_acl_denied",
			"REVOKE EXECUTE ON FUNCTION compound_acl_schema.gin_enum_cmp(anyenum,anyenum) FROM PUBLIC",
			"GRANT EXECUTE ON FUNCTION compound_acl_schema.gin_enum_cmp(anyenum,anyenum) TO compound_acl_reader WITH GRANT OPTION",
			"CREATE INDEX compound_acl_i ON compound_acl_schema.compound_acl_rows USING gin(v compound_acl_schema.enum_ops)",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO compound_acl_schema.compound_acl_rows VALUES(1,'z'),(2,'a'),(3,'a'),(4,NULL)", 4))
		const compare = "SELECT compound_acl_schema.gin_enum_cmp($1::compound_acl_schema.compound_acl_enum,$2::compound_acl_schema.compound_acl_enum) AS v"
		held := classHeld(t, p, conn, compare)
		metadata := func(t *testing.T, rows *sql.Rows) {
			fixture.DriverBasicMetadata(t, rows, b.PG, []string{"INT4"}, []bool{true}, 0)
		}
		checkCalls := func() {
			t.Helper()
			for _, tc := range []struct{ left, right, want any }{{"z", "a", int64(-1)}, {"a", "z", int64(1)}, {"a", "a", int64(0)}, {"m", "z", int64(1)}, {nil, "a", nil}, {"a", nil, nil}, {nil, nil, nil}} {
				step := fixture.Select(compare, []string{"v"}, []string{"INT4"}, [][]any{{tc.want}}, tc.left, tc.right)
				step.CheckMetadata = metadata
				classRoutes(t, p, b, conn, step)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
			}
			empty := fixture.Select(compare+" WHERE false", []string{"v"}, []string{"INT4"}, nil, "z", "a")
			empty.CheckMetadata = metadata
			classRoutes(t, p, b, conn, empty)
		}
		checkDenied := func() {
			t.Helper()
			step := fixture.ErrorDiagnostic(compare, fixture.ControlsReferenceDiagnostic("42501", "permission denied for function gin_enum_cmp"), "z", "a")
			step.SelectRows = true
			step.ErrorStage = "execute"
			classRoutes(t, p, b, conn, step)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
		}
		var queries []string
		var selected []fixture.HeldStatement
		for _, operator := range []string{"<", "<=", "=", ">=", ">"} {
			q := "SELECT id FROM compound_acl_schema.compound_acl_rows WHERE v " + operator + " $1::compound_acl_schema.compound_acl_enum ORDER BY id"
			queries = append(queries, q)
			selected = append(selected, classHeld(t, p, conn, q))
		}
		checkRows := func(wants [][]int64) {
			t.Helper()
			for i, q := range queries {
				step := ginIDs(q, wants[i], "a")
				classRoutes(t, p, b, conn, step)
				fixture.RunStep(t, p.Ctx, selected[i], b.Recorder, step)
				null := ginIDs(q, nil, nil)
				classRoutes(t, p, b, conn, null)
				fixture.RunStep(t, p.Ctx, selected[i], b.Recorder, null)
			}
			classRoutes(t, p, b, conn, ginIDs("SELECT id FROM compound_acl_schema.compound_acl_rows WHERE v IS NULL ORDER BY id", []int64{4}))
		}
		initial := [][]int64{{1}, {1, 2, 3}, {2, 3}, {2, 3}, nil}
		checkRows(initial)
		values := func(want [][]any) {
			classRoutes(t, p, b, conn, compoundValueStep(b, "SELECT id,v FROM compound_acl_schema.compound_acl_rows ORDER BY id", "", 0, want))
		}
		values([][]any{{int64(1), "z"}, {int64(2), "a"}, {int64(3), "a"}, {int64(4), nil}})
		const aclQuery = "SELECT g.rolname AS grantor,r.rolname AS grantee,a.privilege_type,a.is_grantable FROM pg_proc p CROSS JOIN LATERAL aclexplode(p.proacl) a JOIN pg_roles g ON g.oid=a.grantor JOIN pg_roles r ON r.oid=a.grantee WHERE p.oid='compound_acl_schema.gin_enum_cmp(anyenum,anyenum)'::regprocedure"
		checkACL := func(owner string) {
			t.Helper()
			acl := fixture.Select(aclQuery, []string{"grantor", "grantee", "privilege_type", "is_grantable"}, []string{"NAME", "NAME", "TEXT", "BOOL"}, [][]any{{owner, owner, "EXECUTE", false}, {owner, "compound_acl_reader", "EXECUTE", true}})
			acl.Bag = true
			classRoutes(t, p, b, conn, acl)
			classRoutes(t, p, b, conn, fixture.Select("SELECT r.rolname,p.proacl IS NOT NULL AS explicit FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE p.oid='compound_acl_schema.gin_enum_cmp(anyenum,anyenum)'::regprocedure", []string{"rolname", "explicit"}, []string{"NAME", "BOOL"}, [][]any{{owner, true}}))
			run(amCount("SELECT count(*) FROM pg_proc p CROSS JOIN LATERAL aclexplode(p.proacl) a WHERE p.oid='compound_acl_schema.gin_enum_cmp(anyenum,anyenum)'::regprocedure AND a.grantee=0", 0))
		}
		checkACL(installer)
		run(fixture.Exec("SET ROLE compound_acl_reader", 0))
		checkCalls()
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("SET ROLE compound_acl_denied", 0))
		checkDenied()
		run(fixture.Exec("RESET ROLE", 0))
		const alter = "ALTER FUNCTION compound_acl_schema.gin_enum_cmp(anyenum,anyenum) OWNER TO compound_acl_owner"
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec(alter, 0))
		checkACL("compound_acl_owner")
		checkRows(initial)
		run(fixture.Exec("ROLLBACK", 0))
		checkACL(installer)
		checkRows(initial)
		run(fixture.Exec(alter, 0))
		checkACL("compound_acl_owner")
		run(fixture.Exec("SET ROLE compound_acl_owner", 0))
		checkCalls()
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("SET ROLE compound_acl_reader", 0))
		checkCalls()
		run(fixture.Exec("RESET ROLE", 0))
		// Index maintenance uses the installed support capability, not the SQL
		// caller's EXECUTE permission on the helper.
		run(fixture.Exec("SET ROLE compound_acl_denied", 0))
		checkDenied()
		run(fixture.Exec("UPDATE compound_acl_schema.compound_acl_rows SET v='a' WHERE id=1", 1))
		run(fixture.Exec("RESET ROLE", 0))
		checkRows([][]int64{nil, {1, 2, 3}, {1, 2, 3}, {1, 2, 3}, nil})
		run(fixture.Exec("SET ROLE compound_acl_denied", 0))
		run(fixture.Exec("DELETE FROM compound_acl_schema.compound_acl_rows WHERE id=2", 1))
		run(fixture.Exec("RESET ROLE", 0))
		checkRows([][]int64{nil, {1, 3}, {1, 3}, {1, 3}, nil})
		run(fixture.Exec("SET ROLE compound_acl_denied", 0))
		run(fixture.Exec("INSERT INTO compound_acl_schema.compound_acl_rows VALUES(5,'a')", 1))
		checkDenied()
		run(fixture.Exec("RESET ROLE", 0))
		final := [][]int64{nil, {1, 3, 5}, {1, 3, 5}, {1, 3, 5}, nil}
		checkRows(final)
		run(fixture.Exec("SET ROLE compound_acl_owner", 0))
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION compound_acl_schema.gin_enum_cmp(anyenum,anyenum) FROM compound_acl_reader", 0))
		checkCalls()
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("SET ROLE compound_acl_reader", 0))
		checkDenied()
		run(fixture.Exec("RESET ROLE", 0))
		checkRows(final)
		run(fixture.Exec("SET ROLE compound_acl_owner", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION compound_acl_schema.gin_enum_cmp(anyenum,anyenum) TO compound_acl_reader WITH GRANT OPTION", 0))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("SET ROLE compound_acl_reader", 0))
		checkCalls()
		run(fixture.Exec("RESET ROLE", 0))
		checkACL("compound_acl_owner")
		checkRows(final)
		values([][]any{{int64(1), "a"}, {int64(3), "a"}, {int64(4), nil}, {int64(5), "a"}})
		run(amCount("SELECT count(*) FROM pg_index WHERE indexrelid='compound_acl_schema.compound_acl_i'::regclass", 1))
	})
}
