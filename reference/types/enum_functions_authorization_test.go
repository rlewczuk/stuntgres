package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func enumFunctionsAuthorization(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
	t.Helper()
	defer func() {
		if _, err := conn.ExecContext(p.Ctx, "RESET ROLE"); err != nil {
			t.Error(err)
		}
		if _, err := conn.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS enum_function_hidden CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE ROLE enum_function_reader", 0))
	// Native public already grants USAGE; schema GRANT is outside its SQL surface.
	if b.PG {
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(`GRANT USAGE ON SCHEMA "public" TO enum_function_reader`, 0))
	}
	for _, q := range []string{
		"CREATE SCHEMA IF NOT EXISTS enum_function_hidden",
		`CREATE TYPE enum_function_hidden."Odd Type" AS ENUM ('z','a')`,
		"GRANT SELECT(id) ON enum_endpoints TO enum_function_reader",
		"SET ROLE enum_function_reader",
	} {
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(q, 0))
	}
	for _, tc := range []struct{ query, code, message, position, stage string }{
		{"SELECT enum_range(lo,hi) FROM enum_endpoints WHERE id=99", "42501", "permission denied for table enum_endpoints", "", "execute"},
		{"SELECT enum_first(lo) FROM enum_endpoints WHERE id=99", "42501", "permission denied for table enum_endpoints", "", "execute"},
		{"SELECT enum_function_hidden.enum_first(NULL::mood)", "42501", "permission denied for schema enum_function_hidden", "8", "prepare"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			fixture.EnumReferenceErrorChecked(t, p, conn, tc.query, true, tc.stage, func(t *testing.T, got fixture.Diagnostic) {
				want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.position}
				if got != want {
					t.Errorf("authorization diagnostic got %#v want %#v", got, want)
				}
			})
		})
	}
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("RESET ROLE", 0))
	q := `SELECT enum_range(NULL::mood,NULL::enum_function_hidden."Odd Type")`
	fixture.EnumReferenceErrorChecked(t, p, conn, q, true, "prepare", func(t *testing.T, got fixture.Diagnostic) {
		want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: `function enum_range(mood, enum_function_hidden."Odd Type") does not exist`, Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}
		if got != want {
			t.Errorf("hidden-name diagnostic got %#v want %#v", got, want)
		}
	})
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("GRANT SELECT(lo,hi) ON enum_endpoints TO enum_function_reader", 0))
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET ROLE enum_function_reader", 0))
	enumFunctionsSelect(t, p, b, conn, "SELECT enum_range(lo,hi) AS v FROM enum_endpoints WHERE id=1", "{z,a}", true, true)
}
