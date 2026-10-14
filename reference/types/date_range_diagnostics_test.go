package reference_test

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

type dateRangeFailure struct { input, code, message, detail, hint string }

func dateRangeFailures() []dateRangeFailure {
	cases := []dateRangeFailure{}
	for _, tc := range []struct{ input, detail string }{
		{"", "Missing left parenthesis or bracket."},
		{"2000-01-01,2000-01-02)", "Missing left parenthesis or bracket."},
		{"emptyx", `Junk after "empty" key word.`},
		{"[2000-01-01)", "Missing comma after lower bound."},
		{"[2000-01-01,2000-01-02,2000-01-03]", "Too many commas."},
		{"[2000-01-01,2000-01-02", "Unexpected end of input."},
		{`["2000-01-01,2000-01-02)`, "Unexpected end of input."},
		{`[2000-01-01,2000-01-02\`, "Unexpected end of input."},
		{"[2000-01-01,2000-01-02)junk", "Junk after right parenthesis or bracket."},
		{"[bad,2000-01-01)junk", "Junk after right parenthesis or bracket."},
	} { cases = append(cases, dateRangeFailure{input: tc.input, code: "22P02", message: `malformed range literal: "`+tc.input+`"`, detail: tc.detail}) }
	for _, tc := range []struct{ input, decoded string }{
		{"[bad,2000-01-01)", "bad"}, {`["",2000-01-01)`, ""},
		{"[ ,2000-01-01)", " "}, {"[NULL,2000-01-01)", "NULL"},
		{`["2000""01",2000-01-01)`, `2000"01`},
		{"[bad,5874898-01-01]", "bad"}, {"[2000-01-01,bad)", "bad"},
		{"(5874897-12-31,bad]", "bad"},
	} { cases = append(cases, dateRangeFailure{input: tc.input, code: "22007", message: `invalid input syntax for type date: "`+tc.decoded+`"`}) }
	for _, tc := range []struct{ endpoint, hint string }{
		{"1900-02-29", ""}, {"0000-01-01", ""},
		{"2001-13-01", `Perhaps you need a different "DateStyle" setting.`},
		{"2001-01-32", `Perhaps you need a different "DateStyle" setting.`},
	} { cases = append(cases, dateRangeFailure{input: "["+tc.endpoint+",2000-01-01)", code: "22008", message: `date/time field value out of range: "`+tc.endpoint+`"`, hint: tc.hint}) }
	for _, endpoint := range []string{"4714-11-23 BC", "5874898-01-01"} {
		cases = append(cases, dateRangeFailure{input: "["+endpoint+",infinity)", code: "22008", message: `date out of range: "`+endpoint+`"`})
	}
	for _, input := range []string{"[2000-03-01,2000-02-29)", "[infinity,-infinity]", `[5874897-12-31,"4714-11-24 BC"]`} {
		cases = append(cases, dateRangeFailure{input: input, code: "22000", message: "range lower bound must be less than or equal to range upper bound"})
	}
	for _, input := range []string{"[5874897-12-31,5874897-12-31]", "[2000-01-01,5874897-12-31]", "(5874897-12-31,)"} {
		cases = append(cases, dateRangeFailure{input: input, code: "22008", message: "date out of range"})
	}
	return cases
}

func TestReferenceDateRangeDiagnostics(t *testing.T) {
	for i, tc := range dateRangeFailures() {
		t.Run(fmt.Sprintf("%02d", i), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE r(v daterange)", 0))
				want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				want.Detail, want.Hint, want.Position = tc.detail, tc.hint, "29"
				query := "/*é*/ INSERT INTO r VALUES ("+rangeLiteral(tc.input)+")"
				for _, route := range []string{"exec", "query", "prepare", "prepared_query", "bind", "prepared_bind", "query_bind", "prepared_query_bind"} {
					t.Run(route, func(t *testing.T) {
						diagnostic := want
						s := fixture.ErrorDiagnostic(query, diagnostic)
						s.Prepared = strings.HasPrefix(route, "prepare")
						s.SelectRows = strings.Contains(route, "query")
						s.ErrorStage = "execute"
						if s.Prepared { s.ErrorStage = "prepare" }
						if strings.HasSuffix(route, "bind") {
							diagnostic.Position, diagnostic.Context = "", `unnamed portal parameter $1 = '...'`
							if tc.input == "" { diagnostic.Context = `unnamed portal parameter $1 = ''` }
							s = fixture.ErrorDiagnostic("INSERT INTO r VALUES ($1)", diagnostic, tc.input)
							s.Prepared, s.SelectRows, s.ErrorStage = strings.HasPrefix(route, "prepare"), strings.Contains(route, "query"), "execute"
						}
						run(s)
					})
				}
				run(fixture.Exec("INSERT INTO r VALUES ('[2000-01-01,2000-01-02)')", 1))
				atomic := "INSERT INTO r VALUES ('[2000-02-28,2000-03-01]'),("+rangeLiteral(tc.input)+")"
				want.Position = strconv.Itoa(utf8.RuneCountInString(atomic[:strings.LastIndex(atomic, "('")+1])+1)
				for _, prepared := range []bool{false, true} {
					for _, queryMode := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(atomic, want)
						s.Prepared, s.SelectRows, s.ErrorStage = prepared, queryMode, "execute"
						if prepared { s.ErrorStage = "prepare" }
						run(s)
					}
				}
				want.Position, want.Context = "", `unnamed portal parameter $2 = '...'`
				if tc.input == "" { want.Context = `unnamed portal parameter $2 = ''` }
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("INSERT INTO r VALUES ($1),($2)", want, "[2000-02-28,2000-03-01]", tc.input)
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
				}
				run(rangeSelect(b, "SELECT v FROM r", []string{"v"}, []string{"DATERANGE"}, [][]any{{"[2000-01-01,2000-01-02)"}}, []bool{true}))
			}
		})
	}
}

func TestReferenceDateRangeModifiers(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		for _, typ := range []string{"daterange", "pg_catalog.daterange", `"daterange"`} {
			for _, modifier := range []string{"2", "-1", "2147483648", "2,3", "'x'"} {
				for _, prepared := range []bool{false, true} {
					want := fixture.ControlsReferenceDiagnostic("42601", `type modifier is not allowed for type "daterange"`)
					if typ == "pg_catalog.daterange" {
						want.Message = `type modifier is not allowed for type "pg_catalog.daterange"`
					}
					want.Position = "18"
					s := fixture.ErrorDiagnostic("CREATE TABLE m(v "+typ+"("+modifier+"))", want)
					s.Prepared, s.ErrorStage = prepared, "execute"
					if modifier == "'x'" {
						pgWant := want
						want = fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "'x'"`)
						want.Position = strconv.Itoa(utf8.RuneCountInString("CREATE TABLE m(v "+typ+"(")+1)
						s.Diagnostic, s.PostgresDiagnostic = &want, &pgWant
						if b.PG {
							s.Diagnostic = &pgWant
						} else if prepared {
							s.ErrorStage = "prepare"
						}
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			}
		}
		// A successful declaration after every failed attempt proves no partial DDL.
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE m(v daterange)", 0))
	}
}
