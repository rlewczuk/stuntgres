package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func backupDiagnosticPrecedence(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, tc := range []struct{ q, code, message, hint, position string }{
		{"SELECT missing_backup_schema.pg_backup_stop()", "3F000", `schema "missing_backup_schema" does not exist`, "", "8"},
		{"SELECT pg_backup_start(missing_backup_argument)", "42703", `column "missing_backup_argument" does not exist`, "", "24"},
		{"SELECT public.pg_backup_stop()", "42883", "function public.pg_backup_stop() does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", "8"},
		{"SELECT pg_backup_stop(*)", "42809", "pg_backup_stop(*) specified, but pg_backup_stop is not an aggregate function", "", "8"},
		{"SELECT pg_backup_stop() OVER ()", "42809", "OVER specified, but pg_backup_stop is not a window function nor an aggregate function", "", "8"},
		{"SELECT pg_backup_start(DISTINCT NULL)", "42809", "DISTINCT specified, but pg_backup_start is not an aggregate function", "", "8"},
		{"SELECT pg_backup_stop(", "42601", "syntax error at end of input", "", "23"},
	} {
		s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: tc.position})
		s.SelectRows = true
		backupRoutes(t, p, b, c, s)
	}
	for _, q := range []string{"SELECT pg_backup_start(NULL::text,$1::boolean)", "SELECT pg_backup_stop($1::boolean)"} {
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "bad"`, Context: "unnamed portal parameter $1 = '...'"}, "bad")
		s.SelectRows = true
		s.ErrorStage = "execute"
		backupRoutes(t, p, b, c, s)
	}
	q := "SELECT pg_backup_start(NULL::text,1/$1::integer=0)"
	s := backupError(q, "22012", "division by zero", "", 0)
	backupRoutes(t, p, b, c, s)
	backupArithmeticWrappers(t, p, b, c)
}

// Every route asserts the independent error/typed-NULL contract. A separate
// retained handle proves that neither success nor failure leaks across frames.
func backupArithmeticWrappers(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, tc := range []struct {
		query string
		label bool
	}{
		{"SELECT pg_backup_start(NULL::text,abs(1/$1::integer)=0)", false},
		{"SELECT pg_backup_start($1::text,abs(1/$2::integer)=0)", true},
	} {
		step := func(denominator int) fixture.Step {
			args := []any{denominator}
			if tc.label {
				args = []any{nil, denominator}
			}
			if denominator == 0 {
				return backupError(tc.query, "22012", "division by zero", "", args...)
			}
			return backupSelect(b.PG, tc.query, []string{"pg_backup_start"}, []string{"PG_LSN"}, [][]any{{nil}}, args...)
		}
		backupRoutes(t, p, b, c, step(0))
		backupRoutes(t, p, b, c, step(2))
		stmt, err := c.PrepareContext(p.Ctx, tc.query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.query}
		for _, denominator := range []int{0, 2, 0} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, step(denominator))
		}
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
