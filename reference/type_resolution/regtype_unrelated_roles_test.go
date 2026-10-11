package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRegtypeUnrelatedRoles(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"regtype_unrelated_role"})
	fixture.RegtypeScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder, notices *[]fixture.Diagnostic) {
		fixture.RegtypeSetup(t, p, conn, recorder)
		var original int64
		if err := conn.QueryRowContext(p.Ctx, "SELECT 'regtype_a.same'::regtype::bigint").Scan(&original); err != nil {
			t.Fatal(err)
		}
		const query = "SELECT 'same'::regtype::bigint AS oid,'same'::regtype AS r"
		held, err := conn.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		executor := fixture.HeldStatement{Executor: conn, Statement: held, SQL: query}
		fixture.RegtypeRunStep(t, p.Ctx, executor, recorder, fixture.Select(query, []string{"oid", "r"}, []string{"INT8", "REGTYPE"}, [][]any{{original, "same"}}))
		const warningQuery = "SELECT 'timestamp(7)'::regtype AS a"
		*notices = nil
		warning, err := conn.PrepareContext(p.Ctx, warningQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer warning.Close()
		fixture.RegtypeAssertNotices(t, *notices, []fixture.Diagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "TIMESTAMP(7) precision reduced to maximum allowed, 6", Position: "8"}})
		*notices = nil
		warningExecutor := fixture.HeldStatement{Executor: conn, Statement: warning, SQL: warningQuery}
		fixture.RegtypeRunStep(t, p.Ctx, warningExecutor, recorder, fixture.RegtypeSelect(warningQuery, "timestamp without time zone"))
		fixture.RegtypeAssertNotices(t, *notices, nil)
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("DROP DOMAIN regtype_a.same", 0))
		fixture.RegtypeRunStep(t, p.Ctx, executor, recorder, fixture.Select(query, []string{"oid", "r"}, []string{"INT8", "REGTYPE"}, [][]any{{original, fmt.Sprint(original)}}))
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE DOMAIN regtype_a.same AS integer", 0))
		var recreated int64
		if err := conn.QueryRowContext(p.Ctx, "SELECT 'regtype_a.same'::regtype::bigint").Scan(&recreated); err != nil {
			t.Fatal(err)
		}
		if original == 0 || recreated == 0 || original == recreated {
			t.Fatal("drop/recreate must allocate a distinct backend-local identity", original, recreated)
		}
		fixture.RegtypeRunStep(t, p.Ctx, executor, recorder, fixture.Select(query, []string{"oid", "r"}, []string{"INT8", "REGTYPE"}, [][]any{{original, fmt.Sprint(original)}}))
		for _, change := range []string{"CREATE ROLE regtype_unrelated_role", "DROP ROLE regtype_unrelated_role"} {
			*notices = nil
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec(change, 0))
			fixture.RegtypeAssertNotices(t, *notices, nil)
			// Principal, database and search_path are unchanged: unrelated role
			// mutations neither retarget the old literal nor repeat its warning.
			fixture.RegtypeRunStep(t, p.Ctx, executor, recorder, fixture.Select(query, []string{"oid", "r"}, []string{"INT8", "REGTYPE"}, [][]any{{original, fmt.Sprint(original)}}))
			fixture.RegtypeRunStep(t, p.Ctx, warningExecutor, recorder, fixture.RegtypeSelect(warningQuery, "timestamp without time zone"))
			fixture.RegtypeAssertNotices(t, *notices, nil)
			*notices = nil
			// A newly prepared literal still warns once; unrelated role changes
			// must not suppress validation on genuinely new analysis.
			fresh, err := conn.PrepareContext(p.Ctx, warningQuery)
			if err != nil {
				t.Fatal(err)
			}
			fixture.RegtypeAssertNotices(t, *notices, []fixture.Diagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "TIMESTAMP(7) precision reduced to maximum allowed, 6", Position: "8"}})
			*notices = nil
			fixture.RegtypeRunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: fresh, SQL: warningQuery}, recorder, fixture.RegtypeSelect(warningQuery, "timestamp without time zone"))
			fixture.RegtypeAssertNotices(t, *notices, nil)
			fresh.Close()
		}
	})
}

func TestReferenceRegtypeUnrelatedRolesQualifiedAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"regtype_authority_owner_a", "regtype_authority_owner_b"})
	fixture.RegtypeScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder, notices *[]fixture.Diagnostic) {
		for _, query := range []string{
			"CREATE ROLE regtype_authority_owner_a", "CREATE ROLE regtype_authority_owner_b",
			"CREATE SCHEMA regtype_private AUTHORIZATION regtype_authority_owner_a",
			"CREATE DOMAIN regtype_private.secret AS integer", "SET search_path TO public",
			"SET ROLE regtype_authority_owner_a",
		} {
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec(query, 0))
		}
		defer conn.ExecContext(p.Ctx, "RESET ROLE")
		const literal = "SELECT 'regtype_private.secret'::regtype AS a"
		const frame = "SELECT $1::regtype AS r"
		held, err := conn.PrepareContext(p.Ctx, literal)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		input, err := conn.PrepareContext(p.Ctx, frame)
		if err != nil {
			t.Fatal(err)
		}
		defer input.Close()
		executor := fixture.HeldStatement{Executor: conn, Statement: held, SQL: literal}
		fixture.RegtypeRunStep(t, p.Ctx, executor, recorder, fixture.RegtypeSelect(literal, "regtype_private.secret"))
		// Administrative peer mutates relevant qualified-schema USAGE without
		// changing the held connection's principal or effective search path.
		fixture.RunStep(t, p.Ctx, backend.DB, recorder, fixture.Exec("ALTER SCHEMA regtype_private OWNER TO regtype_authority_owner_b", 0))
		want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema regtype_private")
		want.Position = "8"
		rows, err := held.QueryContext(p.Ctx)
		if rows != nil {
			rows.Close()
			t.Fatal("failed qualified literal exposed cursor")
		}
		fixture.AssertDiagnostic(t, err, &want)
		rows, err = input.QueryContext(p.Ctx, "regtype_private.secret")
		if rows != nil {
			rows.Close()
			t.Fatal("failed qualified frame exposed cursor")
		}
		frameWant := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema regtype_private")
		frameWant.Context = "unnamed portal parameter $1 = '...'"
		fixture.AssertDiagnostic(t, err, &frameWant)
		fresh, err := conn.PrepareContext(p.Ctx, literal)
		if fresh != nil {
			fresh.Close()
			t.Fatal("new literal Prepare unexpectedly admitted inaccessible schema")
		}
		if err == nil {
			t.Fatal("new literal Prepare must enforce current schema USAGE")
		}
		fixture.AssertDiagnostic(t, err, &want)
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("SET ROLE regtype_authority_owner_b", 0))
		fixture.RegtypeRunStep(t, p.Ctx, executor, recorder, fixture.RegtypeSelect(literal, "regtype_private.secret"))
		fixture.RegtypeRunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: input, SQL: frame}, recorder, fixture.Select(frame, []string{"r"}, []string{"REGTYPE"}, [][]any{{"regtype_private.secret"}}, "regtype_private.secret"))
		fixture.RunStep(t, p.Ctx, backend.DB, recorder, fixture.Exec("ALTER SCHEMA regtype_private OWNER TO regtype_authority_owner_a", 0))
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("SET ROLE regtype_authority_owner_a", 0))
		fixture.RegtypeRunStep(t, p.Ctx, executor, recorder, fixture.RegtypeSelect(literal, "regtype_private.secret"))
	})
}
