package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const routineLanguageCall = " HANDLER pg_catalog.plpgsql_call_handler"
const routineLanguageFull = routineLanguageCall + " INLINE pg_catalog.plpgsql_inline_handler VALIDATOR pg_catalog.plpgsql_validator"

// Languages are database-global: public-schema cleanup alone is insufficient.
// Register this before mutation, on the borrowed owner, so it runs before Close.
func routineLanguagesCleanup(t *testing.T, c *sql.Conn, statements ...string) func() {
	t.Helper()
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, q := range append([]string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "RESET ROLE", "RESET search_path"}, statements...) {
			if _, err := c.ExecContext(ctx, q); err != nil {
				t.Errorf("language cleanup %q: %v", q, err)
			}
		}
	}
}

// Expectations come from REL_18_6 proclang.c/pg_proc.c, not native results.
// Optional fields are deliberately asserted too; oracle discovery must resolve
// the uncertainties recorded in the implementer handoff before production.
func routineLanguagesError(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, want fixture.Diagnostic, query bool, preparedStage string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, want)
		s.Prepared, s.SelectRows, s.ErrorStage = prepared, query, "execute"
		if prepared {
			s.ErrorStage = preparedStage
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func routineLanguagesMissing(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q, name string) {
	t.Helper()
	d := fixture.ControlsReferenceDiagnostic("42704", fmt.Sprintf("language %q does not exist", name))
	routineLanguagesError(t, p, b, c, q, d, false, "execute")
}

func routineLanguagesCount(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string, want int64) {
	t.Helper()
	s := fixture.Select("SELECT count(*) AS v FROM pg_language WHERE lanname=$1", []string{"v"}, []string{"INT8"}, [][]any{{want}}, name)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

func routineLanguagesOID(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) int64 {
	t.Helper()
	var oid int64
	if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_language WHERE lanname=$1", name).Scan(&oid); err != nil {
		t.Fatal(err)
	}
	return oid
}

func routineLanguagesHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, function string) (func(int, int64), func()) {
	t.Helper()
	q := "SELECT " + function + "($1::integer) AS v"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	return func(input int, want int64) {
		s := fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{want}}, input)
		s.CheckMetadata = parametersMetadata(b, "v", "INT4")
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
	}, func() { stmt.Close() }
}

func routineLanguagesRestoreBuiltin(t *testing.T, c *sql.Conn, from, to string) func() {
	t.Helper()
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := c.ExecContext(ctx, "RESET SESSION AUTHORIZATION"); err != nil {
			t.Error(err)
			return
		}
		var exists bool
		if err := c.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_language WHERE lanname=$1)", from).Scan(&exists); err != nil {
			t.Error(err)
			return
		}
		if exists {
			if _, err := c.ExecContext(ctx, "ALTER LANGUAGE "+from+" RENAME TO "+to); err != nil {
				t.Error(err)
			}
		}
	}
}
