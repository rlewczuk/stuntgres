package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func inspectStoredRecipes(t *testing.T, p *fixture.Pair, b fixture.Backend) []fixture.Step {
	t.Helper()
	checks := []fixture.Step{}
	for i, f := range inspectFamilies {
		name := fmt.Sprintf("inspect_store_%d", i)
		ddl := "CREATE TABLE " + name + "(v " + f.name + ",g " + strings.ToLower(f.subtype) + " GENERATED ALWAYS AS (lower(v)) STORED,CHECK (lower_inc(v) IS NOT NULL OR v IS NULL))"
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(ddl, 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO "+name+"(v) VALUES ("+rangeLiteral(f.finite)+"),('empty'),(NULL)", 3))
		// Explicit independent predicates avoid assuming range ordering.
		check := inspectSelect(b, "SELECT lower(v) AS lo,upper(v) AS hi,range_merge(v,v) AS hull FROM "+name+" WHERE lower(v) IS NOT NULL", []string{"lo", "hi", "hull"}, []string{f.subtype, f.subtype, strings.ToUpper(f.name)}, [][]any{{inspectBound(b, f.subtype, f.lo), inspectBound(b, f.subtype, f.hi), f.hull}})
		inspectRunBoth(t, p, b, check)
		checks = append(checks, check)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE VIEW "+name+"_view AS SELECT lower(range_merge(v,v)) AS lo,upper(range_merge(v,v)) AS hi,range_merge(v,v) AS hull FROM "+name+" WHERE lower(v) IS NOT NULL", 0))
		check = inspectSelectNullable(b, "SELECT lo,hi,hull FROM "+name+"_view", check.Columns, check.Types, check.Rows, []bool{true, true, true})
		inspectRunBoth(t, p, b, check)
		checks = append(checks, check)
		generated := inspectSelectNullable(b, "SELECT g AS lo FROM "+name+" WHERE g IS NOT NULL", []string{"lo"}, []string{f.subtype}, [][]any{{inspectBound(b, f.subtype, f.lo)}}, []bool{true})
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, generated)
		checks = append(checks, generated)
		projection := fmt.Sprintf("inspect_projection_%d", i)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE "+projection+"(lo "+strings.ToLower(f.subtype)+",hi "+strings.ToLower(f.subtype)+",hull "+f.name+")", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO "+projection+" SELECT lower(v),upper(v),range_merge(v,v) FROM "+name+" WHERE lower(v) IS NOT NULL", 1))
		projected := inspectSelectNullable(b, "SELECT lo,hi,hull FROM "+projection, check.Columns, check.Types, check.Rows, []bool{true, true, true})
		inspectRunBoth(t, p, b, projected)
		checks = append(checks, projected)
	}
	for i, f := range inspectFamilies[:3] {
		name := fmt.Sprintf("inspect_multi_%d", i)
		typ := strings.Replace(f.name, "range", "multirange", 1)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE "+name+"(v "+typ+",g "+f.name+" GENERATED ALWAYS AS (range_merge(v)) STORED)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO "+name+"(v) VALUES ('{[8,9),[1,3)}')", 1))
		check := inspectSelect(b, "SELECT lower(v) AS lo,upper(v) AS hi,range_merge(v) AS hull FROM "+name, []string{"lo", "hi", "hull"}, []string{f.subtype, f.subtype, strings.ToUpper(f.name)}, [][]any{{inspectBound(b, f.subtype, "1"), inspectBound(b, f.subtype, "9"), "[1,9)"}})
		inspectRunBoth(t, p, b, check)
		checks = append(checks, check)
		generated := inspectSelectNullable(b, "SELECT g AS lo FROM "+name, []string{"lo"}, []string{strings.ToUpper(f.name)}, [][]any{{"[1,9)"}}, []bool{true})
		inspectRunBoth(t, p, b, generated)
		checks = append(checks, generated)
	}
	return checks
}
func TestReferenceRangeInspectControls(t *testing.T) {
	inspectScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		checks := inspectStoredRecipes(t, p, b)
		failures := []rangeStagedFailure{}
		for _, tc := range []struct{ literal, code, message, detail string }{
			{"bad", "22P02", `malformed range literal: "bad"`, "Missing left parenthesis or bracket."},
			{"[4,1)", "22000", "range lower bound must be less than or equal to range upper bound", ""},
			{"[1,2147483647]", "22003", "integer out of range", ""},
		} {
			q := "CREATE TABLE staged(v int4range,g integer GENERATED ALWAYS AS (lower(v)) STORED); INSERT INTO staged(v) VALUES ('[1,2)'); INSERT INTO staged(v) VALUES (" + rangeLiteral(tc.literal) + ");"
			failures = append(failures, rangeStagedFailure{q, inspectDiagnostic(q, tc.code, tc.message, "", rangeLiteral(tc.literal), tc.detail, "")})
		}
		rangeControlCycle(t, p, b, checks, make([][]bool, len(checks)), failures, inspectReplayStep)
	})
}

// These are conversions of independently encoded DATE expectations, not values
// learned from a backend. pq's DATE transport is time.Time; native DATE is text.
func inspectReplayStep(b fixture.Backend, step fixture.Step) fixture.Step {
	rows := make([][]any, len(step.Rows))
	for i, row := range step.Rows {
		rows[i] = append([]any(nil), row...)
		for j, value := range row {
			if step.Types[j] == "DATE" && value != nil {
				rows[i][j] = inspectBound(b, "DATE", value.(string))
			}
		}
	}
	if strings.HasPrefix(step.Query, "SELECT g AS lo") || strings.HasPrefix(step.Query, "SELECT lo,hi,hull FROM ") {
		nullable := make([]bool, len(step.Types))
		for i := range nullable {
			nullable[i] = true
		}
		return inspectSelectNullable(b, step.Query, step.Columns, step.Types, rows, nullable)
	}
	return inspectSelect(b, step.Query, step.Columns, step.Types, rows)
}
