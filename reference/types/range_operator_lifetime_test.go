package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// A BOOL-only signature can reanalyze against reminted domain operands. Native
// controls have no PostgreSQL API analogue; the ordinary SQL body remains shared.
func operatorControlCycle(t *testing.T, p *fixture.Pair, b fixture.Backend, checks []fixture.Step, nullable [][]bool, failures []rangeStagedFailure) {
	t.Helper()
	q := "SELECT a @> b AS v FROM operator_domains ORDER BY k"
	stmt, err := b.DB.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
	step := fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{true}, {false}, {nil}})
	fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
	rangeControlCycle(t, p, b, checks, nullable, failures)
	fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
	if b.PG {
		return
	}
	// This actual populated nested-domain cursor owns its pre-replacement values,
	// standard metadata and operator provider while the new table changes shape.
	check := checks[0]
	rows, err := b.DB.QueryContext(p.Ctx, check.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	check.CheckMetadata(t, rows)
	script := "CREATE TABLE operator_domains(k integer,a text,b text); INSERT INTO operator_domains VALUES (1,'left','right');"
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString(script)); err != nil {
		t.Fatal(err)
	}
	fixture.RangeConsumeHeld(t, rows, check)
	want := operatorMissing(q, "text", "@>", "text")
	bad := fixture.ErrorDiagnostic(q, want)
	bad.SelectRows = true
	bad.ErrorStage = "execute"
	fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, bad)
	prepared := fixture.Prepared(bad)
	prepared.ErrorStage = "prepare"
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, prepared)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT k,a,b FROM operator_domains", []string{"k", "a", "b"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "left", "right"}}))
}
