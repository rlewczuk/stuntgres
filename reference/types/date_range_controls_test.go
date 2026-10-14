package reference_test

import (
	"strconv"
	"strings"
	"unicode/utf8"
	"stuntgres/reference/fixture"
	"testing"
)

// Actual canonical SQL serialization, atomic staged replacement and retained
// readers exercise the existing public native controls and oracle SQL replay.
func TestReferenceDateRangeControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE r(k integer NOT NULL,v daterange)", 0))
		run(fixture.Exec("CREATE TABLE vacant(v daterange NOT NULL)", 0))
		run(fixture.Exec(`INSERT INTO r VALUES (1,'[2000-02-28,2000-03-01]'),(2,'[0001-12-31 BC,0001-01-01)'),(3,'[294277-01-01,294277-01-02)'),(4,'["4714-11-24 BC",5874897-12-31)'),(5,'[2000-01-01,infinity]'),(6,'[2000-01-01,)'),(7,'empty'),(8,NULL),(9,'[2000-02-28,2000-03-01]')`, 9))
		want := [][]any{
			{int64(1), "[2000-02-28,2000-03-02)"}, {int64(2), `["0001-12-31 BC",0001-01-01)`},
			{int64(3), "[294277-01-01,294277-01-02)"}, {int64(4), `["4714-11-24 BC",5874897-12-31)`},
			{int64(5), "[2000-01-01,infinity]"}, {int64(6), "[2000-01-01,)"},
			{int64(7), "empty"}, {int64(8), nil}, {int64(9), "[2000-02-28,2000-03-02)"},
		}
		for _, prepared := range []bool{false, true} {
			for _, s := range []fixture.Step{
				rangeSelect(b, "SELECT k,v FROM r ORDER BY k", []string{"k", "v"}, []string{"INT4", "DATERANGE"}, want, []bool{false, true}),
				rangeSelect(b, "SELECT v FROM vacant", []string{"v"}, []string{"DATERANGE"}, nil, []bool{false}),
			} { s.Prepared = prepared; run(s) }
		}
		checks := []fixture.Step{
			rangeSelect(b, "SELECT k,v FROM r ORDER BY k", []string{"k", "v"}, []string{"INT4", "DATERANGE"}, want, []bool{false, true}),
			rangeSelect(b, "SELECT v FROM vacant", []string{"v"}, []string{"DATERANGE"}, nil, []bool{false}),
		}
		failures := []dateRangeStagedFailure{}
		for _, tc := range dateRangeFailures() {
			script := "CREATE TABLE staged(v daterange); INSERT INTO staged VALUES ('[epoch,epoch]'); INSERT INTO staged VALUES (" + rangeLiteral(tc.input) + ");"
			diagnostic := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
			diagnostic.Detail, diagnostic.Hint = tc.detail, tc.hint
			diagnostic.Position = strconv.Itoa(utf8.RuneCountInString(script[:strings.LastIndex(script, "('")+1])+1)
			failures = append(failures, dateRangeStagedFailure{script, diagnostic})
		}
		dateRangeControlCycle(t, p, b, checks, [][]bool{{false, true}, {false}}, failures)
	}
}

func TestReferenceDateRangeInputBoundaries(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE r(v daterange)", 0))
		run(fixture.Exec("INSERT INTO r VALUES ('[epoch,epoch]')", 1))
		want := fixture.ControlsReferenceDiagnostic("22P02", `malformed range literal: ""`)
		want.Detail, want.Context = "Missing left parenthesis or bracket.", `unnamed portal parameter $1 = ''`
		for _, prepared := range []bool{false, true} {
			for _, queryMode := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("INSERT INTO r VALUES ($1)", want, []byte{})
				s.Prepared, s.SelectRows, s.ErrorStage = prepared, queryMode, "execute"
				run(s)
			}
		}
		query := "INSERT INTO r VALUES ('[2000-02-28,2000-03-01]'); SELECT ("
		want = fixture.ControlsReferenceDiagnostic("42601", `syntax error at end of input`)
		want.Position = fixture.Position(len(query)+1)
		for _, queryMode := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(query, want)
			s.SelectRows, s.ErrorStage = queryMode, "execute"
			run(s)
		}
		run(rangeSelect(b, "SELECT v FROM r", []string{"v"}, []string{"DATERANGE"}, [][]any{{"[1970-01-01,1970-01-02)"}}, []bool{true}))
	}
}
