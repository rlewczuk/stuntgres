package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAvailableExtensionsDiagnostics(t *testing.T) {
	availableExtensionsCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		cases := []struct{ query, message, position string }{
			{"SELECT missing FROM pg_available_extensions WHERE FALSE", `column "missing" does not exist`, "8"},
			{"SELECT /*é*/ missing FROM pg_available_extension_versions WHERE FALSE", `column "missing" does not exist`, "14"},
			{"SELECT a.missing FROM pg_available_extensions a WHERE FALSE", "column a.missing does not exist", "8"},
			{"SELECT name FROM pg_available_extension_versions WHERE name='available_absent' AND missing IS NULL", `column "missing" does not exist`, "84"},
		}
		for _, tc := range cases {
			want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: tc.message, Position: tc.position}
			// Query (not Exec) must fail without exposing a cursor, including FALSE
			// and absent-package filters. Prepare must fail without a handle.
			rows, err := c.QueryContext(p.Ctx, tc.query)
			if rows != nil {
				rows.Close()
				t.Fatal("invalid column exposed Rows", tc.query)
			}
			fixture.AssertDiagnostic(t, err, &want)
			stmt, err := c.PrepareContext(p.Ctx, tc.query)
			if stmt != nil {
				stmt.Close()
				t.Fatal("invalid column exposed prepared handle", tc.query)
			}
			fixture.AssertDiagnostic(t, err, &want)
			// Keep the established middleware's independent structured-error proof.
			s := fixture.ErrorDiagnostic(tc.query, want)
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, c, r, s)
			s.Prepared, s.ErrorStage = true, "prepare"
			fixture.RunStep(t, p.Ctx, c, r, s)
		}
	})
}
