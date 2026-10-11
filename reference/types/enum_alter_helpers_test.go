package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func enumAlterScenario(t *testing.T, body func(*testing.T, fixture.ControlsReferenceFixture, bool)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			f := fixture.NewControlsReferenceFixture(t, p, b.PG)
			body(t, f, b.PG)
		})
	}
}

func enumAlterExec(t *testing.T, f fixture.ControlsReferenceFixture, c *sql.Conn, q string) {
	t.Helper()
	fixture.RunStep(t, f.Pair.Ctx, c, f.Recorder, fixture.Exec(q, 0))
}

func enumAlterRows(t *testing.T, f fixture.ControlsReferenceFixture, x fixture.Executor, q string, labels ...any) {
	t.Helper()
	rows := make([][]any, len(labels))
	for i, label := range labels {
		rows[i] = []any{label}
	}
	s := fixture.Select(q, []string{"v"}, []string{""}, rows)
	s.CheckMetadata = fixture.EnumReferenceMetadata(f.Recorder.RawBackend == fixture.RawPostgres, false)
	fixture.EnumReferenceRunStep(t, f.Pair.Ctx, x, f.Recorder, s)
}

func enumAlterDiagnostic(code, message string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

// Assert the complete portable diagnostic and actual prepare/execute phase.
// Expectations originate in release source and pinned PostgreSQL observations;
// transaction callers own savepoint recovery.
func enumAlterError(t *testing.T, f fixture.ControlsReferenceFixture, c *sql.Conn, q string, prepared, query bool, phase string, want fixture.Diagnostic, args ...any) {
	t.Helper()
	var stmt *sql.Stmt
	var err error
	stage := "execute"
	if prepared {
		stmt, err = c.PrepareContext(f.Pair.Ctx, q)
		if err != nil {
			stage = "prepare"
		}
		if stmt != nil {
			defer stmt.Close()
		}
	}
	if err == nil {
		if query {
			var rows *sql.Rows
			if stmt != nil {
				rows, err = stmt.QueryContext(f.Pair.Ctx, args...)
			} else {
				rows, err = c.QueryContext(f.Pair.Ctx, q, args...)
			}
			if rows != nil {
				rows.Close()
				t.Errorf("error query exposed rows: %q", q)
			}
		} else {
			var result sql.Result
			if stmt != nil {
				result, err = stmt.ExecContext(f.Pair.Ctx, args...)
			} else {
				result, err = c.ExecContext(f.Pair.Ctx, q, args...)
			}
			if result != nil {
				t.Errorf("error exec exposed result: %q", q)
			}
		}
	}
	if err == nil {
		t.Errorf("query=%q prepared=%t unexpectedly succeeded", q, prepared)
		return
	}
	got := fixture.ExtractDiagnostic(t, err)
	if stage != phase || got != want {
		t.Errorf("query=%q prepared=%t got phase=%s diagnostic=%#v; want phase=%s diagnostic=%#v", q, prepared, stage, got, phase, want)
	}
}

func enumAlterUnsafe(label, typ string) fixture.Diagnostic {
	d := enumAlterDiagnostic("55P04", "unsafe use of new value \""+label+"\" of enum type "+typ)
	d.Hint = "New enum values must be committed before they can be used."
	return d
}
