package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func enumAlterClosureHold(t *testing.T, f fixture.ControlsReferenceFixture, steps []fixture.Step) []fixture.HeldStatement {
	t.Helper()
	held := make([]fixture.HeldStatement, len(steps))
	for i, s := range steps {
		stmt, err := f.A.PrepareContext(f.Pair.Ctx, s.Query)
		if err != nil {
			t.Fatalf("prepare before ADD %q: %v", s.Query, err)
		}
		t.Cleanup(func() { stmt.Close() })
		held[i] = fixture.HeldStatement{Executor: f.A, Statement: stmt, SQL: s.Query}
	}
	return held
}

func enumAlterClosureHeldError(t *testing.T, f fixture.ControlsReferenceFixture, h fixture.HeldStatement, query bool, want fixture.Diagnostic) {
	t.Helper()
	var err error
	if query {
		var rows *sql.Rows
		rows, err = h.Statement.QueryContext(f.Pair.Ctx)
		if rows != nil {
			rows.Close()
			t.Errorf("unsafe held query exposed rows: %q", h.SQL)
		}
	} else {
		var result sql.Result
		result, err = h.Statement.ExecContext(f.Pair.Ctx)
		if result != nil {
			t.Errorf("unsafe held mutation exposed result: %q", h.SQL)
		}
	}
	if err == nil {
		t.Errorf("unsafe held statement succeeded: %q", h.SQL)
		return
	}
	if got := fixture.ExtractDiagnostic(t, err); got != want {
		t.Errorf("held Execute %q got %#v; want %#v", h.SQL, got, want)
	}
}

// Every handle was admitted before ADD. Fresh preparation must still succeed;
// all three routes fail at Execute, with no exposed result and full diagnostics.
func enumAlterClosureFailures(t *testing.T, f fixture.ControlsReferenceFixture, steps []fixture.Step, held []fixture.HeldStatement, want fixture.Diagnostic) {
	t.Helper()
	for i, s := range steps {
		for route := range 3 {
			enumAlterExec(t, f, f.A, "SAVEPOINT closure_consumer")
			if route == 2 {
				enumAlterClosureHeldError(t, f, held[i], s.SelectRows, want)
			} else {
				enumAlterError(t, f, f.A, s.Query, route == 1, s.SelectRows, "execute", want)
			}
			enumAlterExec(t, f, f.A, "ROLLBACK TO closure_consumer")
			enumAlterExec(t, f, f.A, "RELEASE closure_consumer")
		}
	}
}

func enumAlterClosureStep(t *testing.T, f fixture.ControlsReferenceFixture, x fixture.Executor, s fixture.Step, pg bool) {
	t.Helper()
	if pg && strings.HasPrefix(s.Query, "MERGE INTO ") {
		// Existing lib/pq MERGE command-tag transport exports affected0. Actual
		// target rows are independently asserted by the scenario, not this tag.
		s.Affected = 0
	}
	fixture.RunStep(t, f.Pair.Ctx, x, f.Recorder, s)
}

// Isolate successful mutations so direct/fresh/held routes see the same rows.
func enumAlterClosureSuccesses(t *testing.T, f fixture.ControlsReferenceFixture, steps []fixture.Step, held []fixture.HeldStatement, pg bool) {
	t.Helper()
	for i, s := range steps {
		for route := range 3 {
			enumAlterExec(t, f, f.A, "SAVEPOINT closure_success")
			var x fixture.Executor = f.A
			s.Prepared = route == 1
			if route == 2 {
				x = held[i]
			}
			enumAlterClosureStep(t, f, x, s, pg)
			enumAlterExec(t, f, f.A, "ROLLBACK TO closure_success")
			enumAlterExec(t, f, f.A, "RELEASE closure_success")
		}
	}
}
