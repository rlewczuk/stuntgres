package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Inputs and outputs are independently specified from PostgreSQL's DATE
// canonicalizer, not collected from either backend.
func dateRangeCases(t *testing.T, cases [][2]string) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE r(k integer NOT NULL,v pg_catalog.\"daterange\")", 0))
		values := make([]string, len(cases))
		want := make([][]any, len(cases))
		for i, tc := range cases {
			values[i] = fmt.Sprintf("(%d,%s)", i, rangeLiteral(tc[0]))
			want[i] = []any{int64(i), tc[1]}
		}
		run(fixture.Exec("INSERT INTO r VALUES "+strings.Join(values, ","), int64(len(cases))))
		run(fixture.Exec("INSERT INTO r VALUES (999,NULL)", 1))
		want = append(want, []any{int64(999), nil})
		for _, prepared := range []bool{false, true} {
			s := rangeSelect(b, "SELECT k,v FROM r ORDER BY k", []string{"k", "v"}, []string{"INT4", "DATERANGE"}, want, []bool{false, true})
			s.Prepared = prepared
			run(s)
		}
	}
}

func TestReferenceDateRangeValues(t *testing.T) {
	dateRangeCases(t, [][2]string{
		{"[2000-02-28,2000-03-01]", "[2000-02-28,2000-03-02)"},
		{"(2000-02-28,2000-03-01)", "[2000-02-29,2000-03-01)"},
		{"(2000-02-28,2000-03-01]", "[2000-02-29,2000-03-02)"},
		{"[2000-02-29,2000-02-29]", "[2000-02-29,2000-03-01)"},
		{"[2000-02-29,2000-02-29)", "empty"},
		{"(2000-02-29,2000-02-29]", "empty"},
		{"(2000-02-29,2000-02-29)", "empty"},
		{"(2000-02-28,2000-02-29)", "empty"},
		{"(1900-02-28,1900-03-01)", "empty"},
		{"[1999-12-31,1999-12-31]", "[1999-12-31,2000-01-01)"},
		{"[0001-12-31 BC,0001-01-01)", `["0001-12-31 BC",0001-01-01)`},
		{"(0001-12-31 BC,0001-01-01]", "[0001-01-01,0001-01-02)"},
		{"[epoch,epoch]", "[1970-01-01,1970-01-02)"},
		{"empty", "empty"}, {" \tEMPTY\r\n", "empty"},
		{"[,]", "(,)"}, {"(,]", "(,)"}, {"[,)", "(,)"}, {"(,)", "(,)"},
		{"[,2000-02-29]", "(,2000-03-01)"}, {"[2000-02-29,]", "[2000-02-29,)"},
	})
}

func TestReferenceDateRangeCanonicalBoundaries(t *testing.T) {
	dateRangeCases(t, [][2]string{
		{`["4714-11-24 BC",5874897-12-31)`, `["4714-11-24 BC",5874897-12-31)`},
		{`("4714-11-24 BC",5874897-12-31)`, `["4714-11-25 BC",5874897-12-31)`},
		{"[5874897-12-31,)", "[5874897-12-31,)"},
		{"[5874897-12-31,infinity)", "[5874897-12-31,infinity)"},
		{"(,4714-11-24 BC)", `(,"4714-11-24 BC")`},
		{"[5874897-12-31,5874897-12-31)", "empty"},
		{"(5874897-12-31,5874897-12-31]", "empty"},
	})
}

func TestReferenceDateRangeInfinity(t *testing.T) {
	dateRangeCases(t, [][2]string{
		{"[2000-01-01,infinity]", "[2000-01-01,infinity]"},
		{"[2000-01-01,infinity)", "[2000-01-01,infinity)"},
		{"[2000-01-01,)", "[2000-01-01,)"},
		{"[-infinity,infinity]", "[-infinity,infinity]"},
		{"(-infinity,infinity)", "(-infinity,infinity)"},
		{"[infinity,infinity]", "[infinity,infinity]"},
		{"[-infinity,-infinity]", "[-infinity,-infinity]"},
		{"[infinity,infinity)", "empty"}, {"(infinity,infinity]", "empty"},
		{"(infinity,)", "(infinity,)"}, {"(,-infinity)", "(,-infinity)"},
	})
}

func TestReferenceDateRangeInputs(t *testing.T) {
	dateRangeCases(t, [][2]string{
		{`["2000-02-28","2000-03-01"]`, "[2000-02-28,2000-03-02)"},
		{`[2000\-02-28,2000\-03-01)`, "[2000-02-28,2000-03-01)"},
		{"[Feb 28 2000,March 1 2000)", "[2000-02-28,2000-03-01)"},
		{"[20000228,20000301)", "[2000-02-28,2000-03-01)"},
		{"[2000-02-28 23:59:59+14,2000-03-01 00:00:00-12)", "[2000-02-28,2000-03-01)"},
		{"[294277-01-01,294277-01-02)", "[294277-01-01,294277-01-02)"},
	})
}
