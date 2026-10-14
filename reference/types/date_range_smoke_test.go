package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceDateRangeFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE r(k integer NOT NULL,v daterange)", 0))
		run(fixture.Exec("CREATE TABLE vacant(v daterange NOT NULL)", 0))
		run(fixture.Exec("INSERT INTO r VALUES (1,'[2000-02-28,2000-03-01]')", 1))
		query := "INSERT INTO r VALUES ($1,$2)"
		stmt, err := b.DB.PrepareContext(p.Ctx, query)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
		for _, row := range [][]any{{2, "[2000-01-01,infinity]"}, {3, "[2000-01-01,)"}, {4, nil}} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(query, 1, row...))
		}
		want := [][]any{{int64(1), "[2000-02-28,2000-03-02)"}, {int64(2), "[2000-01-01,infinity]"}, {int64(3), "[2000-01-01,)"}, {int64(4), nil}}
		check := rangeSelect(b, "SELECT k,v FROM r ORDER BY k", []string{"k", "v"}, []string{"INT4", "DATERANGE"}, want, []bool{false, true})
		vacant := rangeSelect(b, "SELECT v FROM vacant", []string{"v"}, []string{"DATERANGE"}, nil, []bool{false})
		run(check)
		run(fixture.Prepared(check))
		run(vacant)
		failures := []rangeStagedFailure{}
		for _, tc := range []dateRangeFailure{
			{input: "[bad,2000-01-01)", code: "22007", message: `invalid input syntax for type date: "bad"`},
			{input: "[1900-02-29,2000-01-01)", code: "22008", message: `date/time field value out of range: "1900-02-29"`},
			{input: "[2000-03-01,2000-02-29)", code: "22000", message: "range lower bound must be less than or equal to range upper bound"},
			{input: "[5874897-12-31,5874897-12-31]", code: "22008", message: "date out of range"},
		} {
			wantError := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
			wantError.Context = `unnamed portal parameter $2 = '...'`
			for _, prepared := range []bool{false, true} {
				for _, queryMode := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("INSERT INTO r VALUES (90,$1),(91,$2)", wantError, "[epoch,epoch]", tc.input)
					s.Prepared, s.SelectRows, s.ErrorStage = prepared, queryMode, "execute"
					run(s)
					run(check)
				}
			}
			script := "CREATE TABLE staged(v daterange); INSERT INTO staged VALUES ('[epoch,epoch]'); INSERT INTO staged VALUES ("+rangeLiteral(tc.input)+");"
			wantError.Context = ""
			wantError.Position = strconv.Itoa(utf8.RuneCountInString(script[:strings.LastIndex(script, "('")+1])+1)
			failures = append(failures, rangeStagedFailure{script, wantError})
		}
		for _, q := range []string{"CREATE DOMAIN span AS daterange", "CREATE DOMAIN chain AS span", "CREATE TABLE guard(k integer NOT NULL,v chain NOT NULL)"} { run(fixture.Exec(q, 0)) }
		run(fixture.Prepared(fixture.Exec("INSERT INTO guard VALUES ($1,$2)", 1, 1, "[epoch,epoch]")))
		guard := rangeSelect(b, "SELECT k,v FROM guard ORDER BY k", []string{"k", "v"}, []string{"INT4", "DATERANGE"}, [][]any{{int64(1), "[1970-01-01,1970-01-02)"}}, []bool{false, false})
		wantError := fixture.ControlsReferenceDiagnostic("23502", `null value in column "v" of relation "guard" violates not-null constraint`)
		wantError.Detail, wantError.Schema, wantError.Table, wantError.Column = "Failing row contains (2, null).", "public", "guard", "v"
		run(fixture.ErrorDiagnostic("INSERT INTO guard VALUES (0,'empty'),(2,NULL)", wantError))
		run(guard)
		failures = append(failures, rangeStagedFailure{"CREATE DOMAIN span AS daterange; CREATE DOMAIN chain AS span; CREATE TABLE guard(k integer NOT NULL,v chain NOT NULL); INSERT INTO guard VALUES (1,'[epoch,epoch]'); INSERT INTO guard VALUES (2,NULL);", wantError})
		if err := stmt.Close(); err != nil { t.Fatal(err) }
		rangeControlCycle(t, p, b, []fixture.Step{check, vacant, guard}, [][]bool{{false, true}, {false}, {false, false}}, failures)
	}
}
