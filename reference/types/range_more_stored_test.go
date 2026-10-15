package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)


func moreStored(t *testing.T, p *fixture.Pair, b fixture.Backend) []fixture.Step {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
	run(fixture.Exec("CREATE DOMAIN more_stored_base AS int4range", 0))
	run(fixture.Exec("CREATE DOMAIN more_stored_outer AS more_stored_base", 0))
	run(fixture.Exec("CREATE TABLE more_stored(k integer,a more_stored_outer,m int4multirange GENERATED ALWAYS AS (multirange(a)) STORED)", 0))
	run(fixture.Exec("INSERT INTO more_stored(k,a) VALUES (1,'[1,4]'),(2,'empty'),(3,NULL)", 3))
	run(fixture.Exec("CREATE VIEW more_scalar_view AS SELECT k,multirange(a) AS m FROM more_stored", 0))
	run(fixture.Exec("CREATE VIEW more_set_view AS SELECT unnest(m) AS r FROM more_stored", 0))
	run(fixture.Exec("CREATE VIEW more_nested_view AS SELECT multirange(unnest(m)) AS m FROM more_stored", 0))
	run(fixture.Exec("CREATE TABLE more_ctas AS SELECT unnest(m) AS r FROM more_stored", 1))
	run(fixture.Exec("CREATE TABLE more_insert(m int4multirange)", 0))
	run(fixture.Exec("INSERT INTO more_insert SELECT multirange(a) FROM more_stored ORDER BY k", 3))
	run(fixture.Exec("CREATE TABLE more_empty_ctas AS SELECT unnest(m) AS r FROM more_stored WHERE k=2", 0))
	run(fixture.Exec("CREATE TABLE more_check(k integer,a int4range,CONSTRAINT more_only_empty CHECK (isempty(multirange(a))))", 0))
	run(fixture.Exec("INSERT INTO more_check VALUES (0,'empty')", 1))
	checks := []fixture.Step{
		rangeSelect(b, "SELECT m FROM more_stored ORDER BY k", []string{"m"}, []string{"INT4MULTIRANGE"}, [][]any{{"{[1,5)}"}, {"{}"}, {nil}}, []bool{true}),
		rangeSelect(b, "SELECT m FROM more_scalar_view ORDER BY k", []string{"m"}, []string{"INT4MULTIRANGE"}, [][]any{{"{[1,5)}"}, {"{}"}, {nil}}, []bool{true}),
		rangeSelect(b, "SELECT r FROM more_set_view", []string{"r"}, []string{"INT4RANGE"}, [][]any{{"[1,5)"}}, []bool{true}),
		rangeSelect(b, "SELECT m FROM more_nested_view", []string{"m"}, []string{"INT4MULTIRANGE"}, [][]any{{"{[1,5)}"}}, []bool{true}),
		rangeSelect(b, "SELECT r FROM more_ctas", []string{"r"}, []string{"INT4RANGE"}, [][]any{{"[1,5)"}}, []bool{true}),
		rangeSelect(b, "SELECT r FROM more_empty_ctas", []string{"r"}, []string{"INT4RANGE"}, [][]any{}, []bool{true}),
		rangeSelect(b, "SELECT a FROM more_check", []string{"a"}, []string{"INT4RANGE"}, [][]any{{"empty"}}, []bool{true}),
	}
	for _, s := range checks { moreBoth(t, p, b, s) }
	s := rangeSelect(b, "SELECT m FROM more_insert", []string{"m"}, []string{"INT4MULTIRANGE"}, [][]any{{"{[1,5)}"}, {"{}"}, {nil}}, []bool{true})
	s.Bag = true
	moreBoth(t, p, b, s)
	checks = append(checks, s)
	return checks
}

func TestReferenceRangeMoreStored(t *testing.T) {
	moreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		moreStored(t, p, b)
		want := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "more_check" violates check constraint "more_only_empty"`)
		want.Detail = "Failing row contains (2, [1,5))."
		want.Schema, want.Table, want.Constraint = "public", "more_check", "more_only_empty"
		for _, q := range []string{
			"INSERT INTO more_check VALUES (2,'[1,4]')",
			"INSERT INTO more_check SELECT k,a FROM more_stored WHERE k=1",
		} {
			if q == "INSERT INTO more_check SELECT k,a FROM more_stored WHERE k=1" { want.Detail = "Failing row contains (1, [1,5))." }
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, want)
				s.Prepared, s.ErrorStage = prepared, "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
		// The first row satisfies CHECK; a later row must abort the entire write.
		q := "INSERT INTO more_check SELECT k,a FROM more_stored ORDER BY k DESC"
		want.Detail = "Failing row contains (1, [1,5))."
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.ErrorDiagnostic(q, want))
		moreBoth(t, p, b, rangeSelect(b, "SELECT a FROM more_check", []string{"a"}, []string{"INT4RANGE"}, [][]any{{"empty"}}, []bool{true}))
	})
}

func TestReferenceRangeMoreControls(t *testing.T) {
	moreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		checks := moreStored(t, p, b)
		q := "CREATE TABLE more_stage(a int4range,m int4multirange GENERATED ALWAYS AS(multirange(a)) STORED); INSERT INTO more_stage(a) VALUES ('empty'); INSERT INTO more_stage(a) VALUES ('bad');"
		failures := []rangeStagedFailure{{q, inspectDiagnostic(q, "22P02", `malformed range literal: "bad"`, "", "'bad'", "Missing left parenthesis or bracket.", "")}}
		nullable := make([][]bool, len(checks))
		for i := range nullable { nullable[i] = []bool{true} }
		rangeControlCycle(t, p, b, checks, nullable, failures, func(target fixture.Backend, step fixture.Step) fixture.Step {
			s := rangeSelect(target, step.Query, step.Columns, step.Types, step.Rows, []bool{true})
			s.Bag = step.Bag
			return s
		})
		moreHeldLifecycle(t, p, b)
	})
}
