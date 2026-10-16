package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGCursorsDiagnostics(t *testing.T) {
	pgcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "DECLARE pgc_outside NO SCROLL CURSOR FOR SELECT 1"
		sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic(q, sqlCursorDiagnostic("25P01", "DECLARE CURSOR can only be used in transaction blocks")))
		pgcEmpty(t, p, b, c, "pgc_outside")
		sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
		q = "DECLARE pgc_diag NO SCROLL CURSOR FOR SELECT 1 AS i"
		sqlCursorStep(t, p, b, c, fixture.Exec(q, 0))
		original := pgcOne(t, p, b, c, "pgc_diag", q, false, false)
		sqlCursorError(t, p, b, c, "DECLARE pgc_diag SCROLL CURSOR WITH HOLD FOR SELECT 2", sqlCursorDiagnostic("42P03", "cursor \"pgc_diag\" already exists"))
		pgcSame(t, p, b, c, original)
		for _, command := range []string{"FETCH ALL FROM pgc_missing", "MOVE 1 FROM pgc_missing", "CLOSE pgc_missing"} {
			sqlCursorError(t, p, b, c, command, sqlCursorMissing("pgc_missing"))
			pgcEmpty(t, p, b, c, "pgc_missing")
			pgcSame(t, p, b, c, original)
		}
		forward := sqlCursorDiagnostic("55000", "cursor can only scan forward")
		forward.Hint = "Declare it with SCROLL option to enable backward scan."
		sqlCursorError(t, p, b, c, "FETCH BACKWARD FROM pgc_diag", forward)
		pgcSame(t, p, b, c, original)
		empty := sqlCursorDiagnostic("42601", `zero-length delimited identifier at or near """"`)
		empty.Position = "9"
		sqlCursorError(t, p, b, c, `DECLARE "" NO SCROLL CURSOR FOR SELECT 1`, empty)
		pgcSame(t, p, b, c, original)
		for _, tc := range []struct{ query, verb, gerund, action string }{
			{"INSERT INTO pg_cursors(name) VALUES('phantom')", "insert into", "inserting into", "INSERT"},
			{"UPDATE pg_cursors SET name='phantom'", "update", "updating", "UPDATE"},
			{"DELETE FROM pg_cursors", "delete from", "deleting from", "DELETE"},
		} {
			want := sqlCursorDiagnostic("55000", "cannot "+tc.verb+" view \"pg_cursors\"")
			want.Detail = "Views that do not select from a single table or view are not automatically updatable."
			want.Hint = "To enable " + tc.gerund + " the view, provide an INSTEAD OF " + tc.action + " trigger or an unconditional ON " + tc.action + " DO INSTEAD rule."
			sqlCursorError(t, p, b, c, tc.query, want)
			pgcSame(t, p, b, c, original)
			pgcEmpty(t, p, b, c, "phantom")
		}
		missingColumn := sqlCursorDiagnostic("42703", `column "missing" of relation "pg_cursors" does not exist`)
		missingColumn.Position = "24"
		sqlCursorError(t, p, b, c, "INSERT INTO pg_cursors(missing) VALUES('x')", missingColumn)
		// Once aborted, the ordinary transaction diagnostic wins over lookup.
		sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic("CLOSE pgc_missing", sqlCursorMissing("pgc_missing")))
		sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic("CLOSE pgc_diag", sqlCursorDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
		sqlCursorStep(t, p, b, c, fixture.Exec("ROLLBACK", 0))
		pgcEmpty(t, p, b, c, "pgc_diag", "pgc_missing")
	})
	t.Run("quoted_and_clipped_names", func(t *testing.T) {
		pgcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
			var notices []fixture.Diagnostic
			_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			run := func(q string) { fixture.RunStep(t, p.Ctx, c, recorder, fixture.Exec(q, 0)) }
			run("BEGIN")
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			long := strings.Repeat("é", 32)
			clipped := strings.Repeat("é", 31)
			q := "DECLARE \"" + long + "\" NO SCROLL CURSOR FOR SELECT 1"
			run(q)
			want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: "identifier \"" + long + "\" will be truncated to \"" + clipped + "\""}
			if len(notices) != 1 || notices[0] != want {
				t.Fatalf("truncation notices %#v want %#v", notices, want)
			}
			pgcOne(t, p, b, c, clipped, q, false, false)
			run("CLOSE \"" + clipped + "\"")
			pgcEmpty(t, p, b, c, clipped)
			upper := "DECLARE \"PgcCase\" SCROLL CURSOR FOR SELECT 1"
			lower := "DECLARE pgccase NO SCROLL CURSOR FOR SELECT 2"
			run(upper)
			run(lower)
			pgcOne(t, p, b, c, "PgcCase", upper, false, true)
			pgcOne(t, p, b, c, "pgccase", lower, false, false)
			run("CLOSE \"PgcCase\"")
			pgcEmpty(t, p, b, c, "PgcCase")
			pgcOne(t, p, b, c, "pgccase", lower, false, false)
		})
	})
}
