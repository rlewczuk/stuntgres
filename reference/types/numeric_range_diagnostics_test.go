package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func numericRangeFailures() []integerRangeFailure {
	cases := []integerRangeFailure{}
	// The grammar is shared with integer ranges, independently specified there.
	for _, tc := range integerRangeFailures("int4range") {
		if tc.detail != "" { cases = append(cases, tc) }
	}
	for _, tc := range []struct{ input, decoded string }{
		{"[oops,4)", "oops"}, {`["",4)`, ""}, {"[ ,4)", " "}, {"[NULL,4)", "NULL"},
		{"[+NaN,4)", "+NaN"}, {"[-NaN,4)", "-NaN"}, {"[1__2,14)", "1__2"},
		{"[oops,1e131072]", "oops"}, {`["1""2",4)`, `1"2`},
	} {
		cases = append(cases, integerRangeFailure{tc.input, "22P02", `invalid input syntax for type numeric: "`+tc.decoded+`"`, ""})
	}
	for _, input := range []string{"[1e131072,2)", "[0,1e-16384)", "[1e131072,oops]", "[2,1e-16384)"} {
		cases = append(cases, integerRangeFailure{input, "22003", "value overflows numeric format", ""})
	}
	for _, input := range []string{"[4.01,4.00)", "[Infinity,-Infinity]", "[NaN,Infinity]", "[9007199254740992.0000000000000002,9007199254740992.0000000000000001]"} {
		cases = append(cases, integerRangeFailure{input, "22000", "range lower bound must be less than or equal to range upper bound", ""})
	}
	return cases
}

func numericRangeDiagnostic(tc integerRangeFailure) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Detail: tc.detail}
}

func TestReferenceNumericRangeDiagnostics(t *testing.T) {
	for i, tc := range numericRangeFailures() {
		t.Run(fmt.Sprintf("%02d", i), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE r(v numrange)", 0))
				for _, route := range []string{"exec", "prepare", "query", "bind", "prepared_bind"} {
					t.Run(route, func(t *testing.T) {
						want := numericRangeDiagnostic(tc)
						want.Position = "29"
						s := fixture.ErrorDiagnostic("/*é*/ INSERT INTO r VALUES ("+rangeLiteral(tc.input)+")", want)
						s.ErrorStage = "execute"
						switch route {
						case "prepare": s.Prepared, s.ErrorStage = true, "prepare"
						case "query": s.SelectRows = true
						case "bind", "prepared_bind":
							want.Position = ""
							want.Context = `unnamed portal parameter $1 = '...'`
							if tc.input == "" { want.Context = `unnamed portal parameter $1 = ''` }
							s = fixture.ErrorDiagnostic("INSERT INTO r VALUES ($1)", want, tc.input)
							s.Prepared, s.ErrorStage = route == "prepared_bind", "execute"
						}
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					})
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO r VALUES ('[0,1)')", 1))
				for _, prepared := range []bool{false, true} {
					want := numericRangeDiagnostic(tc)
					want.Context = `unnamed portal parameter $1 = '...'`
					if tc.input == "" { want.Context = `unnamed portal parameter $1 = ''` }
					s := fixture.ErrorDiagnostic("INSERT INTO r VALUES ('[1,4]'),($1)", want, tc.input)
					s.Prepared, s.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, rangeSelect(b, "SELECT v FROM r", []string{"v"}, []string{"NUMRANGE"}, [][]any{{"[0,1)"}}, []bool{true}))
				}
			}
		})
	}
}

func TestReferenceNumericRangeModifiers(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		for _, prepared := range []bool{false, true} {
			want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `type modifier is not allowed for type "numrange"`, Position: "18"}
			s := fixture.ErrorDiagnostic("CREATE TABLE m(v numrange(2))", want)
			s.Prepared, s.ErrorStage = prepared, "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
	}
}
