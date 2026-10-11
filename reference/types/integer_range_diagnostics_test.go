package reference_test

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

type integerRangeFailure struct{ input, code, message, detail string }

func integerRangeFailures(typ string) []integerRangeFailure {
	cases := []integerRangeFailure{}
	for _, grammar := range []struct{ input, detail string }{
		{"", "Missing left parenthesis or bracket."}, {"1,4)", "Missing left parenthesis or bracket."},
		{"-[1,4)", "Missing left parenthesis or bracket."}, {"emptyx", `Junk after "empty" key word.`},
		{"[1)", "Missing comma after lower bound."}, {"()", "Missing comma after lower bound."},
		{"[1,2,3]", "Too many commas."}, {"[1,4", "Unexpected end of input."},
		{`["1,4)`, "Unexpected end of input."}, {`[1,4\`, "Unexpected end of input."},
		{"[1,4)junk", "Junk after right parenthesis or bracket."}, {"[1,4))", "Junk after right parenthesis or bracket."},
		{"[oops,4", "Unexpected end of input."}, {"[oops,4)junk", "Junk after right parenthesis or bracket."},
	} {
		cases = append(cases, integerRangeFailure{grammar.input, "22P02", `malformed range literal: "` + grammar.input + `"`, grammar.detail})
	}
	integer, max, min, tooHigh, tooLow := "integer", "2147483647", "-2147483648", "2147483648", "-2147483649"
	if typ == "int8range" {
		integer, max, min, tooHigh, tooLow = "bigint", "9223372036854775807", "-9223372036854775808", "9223372036854775808", "-9223372036854775809"
	}
	for _, subtype := range []struct{ input, decoded string }{
		{"[oops,4)", "oops"}, {`["",4)`, ""}, {"[ ,4)", " "}, {"[NULL,4)", "NULL"},
		{"[oops," + tooHigh + "]", "oops"}, {`["1""2",4)`, `1"2`}, {"[(1,4)", "(1"},
	} {
		cases = append(cases, integerRangeFailure{subtype.input, "22P02", `invalid input syntax for type ` + integer + `: "` + subtype.decoded + `"`, ""})
	}
	cases = append(cases,
		integerRangeFailure{"[1," + tooHigh + ")", "22003", `value "` + tooHigh + `" is out of range for type ` + integer, ""},
		integerRangeFailure{"[" + tooLow + ",4)", "22003", `value "` + tooLow + `" is out of range for type ` + integer, ""},
		integerRangeFailure{"[1," + max + "]", "22003", integer + " out of range", ""},
		integerRangeFailure{"[" + max + "," + max + "]", "22003", integer + " out of range", ""},
		integerRangeFailure{"(" + max + ",)", "22003", integer + " out of range", ""},
		integerRangeFailure{"[4,1)", "22000", "range lower bound must be less than or equal to range upper bound", ""},
		integerRangeFailure{"[" + max + "," + min + "]", "22000", "range lower bound must be less than or equal to range upper bound", ""},
	)
	return cases
}

func TestReferenceIntegerRangeDiagnostics(t *testing.T) {
	for _, typ := range []string{"int4range", "int8range"} {
		for i, tc := range integerRangeFailures(typ) {
			t.Run(fmt.Sprintf("%s/%02d", typ, i), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE r(v "+typ+")", 0))
					want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Detail: tc.detail}
					query := "/*é*/ INSERT INTO r VALUES (" + integerRangeLiteral(tc.input) + ")"
					// Rune counting, not byte offsets: the opener is character29/byte30.
					want.Position = "29"
					for _, route := range []string{"exec", "prepare", "query", "bind", "prepared_bind"} {
						t.Run(route, func(t *testing.T) {
							diagnostic := want
							s := fixture.ErrorDiagnostic(query, diagnostic)
							s.ErrorStage = "execute"
							switch route {
							case "prepare":
								s.Prepared, s.ErrorStage = true, "prepare"
							case "query":
								s.SelectRows = true
							case "bind", "prepared_bind":
								diagnostic.Position = ""
								diagnostic.Context = `unnamed portal parameter $1 = '...'`
								if tc.input == "" {
									diagnostic.Context = `unnamed portal parameter $1 = ''`
								}
								s = fixture.ErrorDiagnostic("INSERT INTO r VALUES ($1)", diagnostic, tc.input)
								s.Prepared = route == "prepared_bind"
								s.ErrorStage = "execute"
							}
							fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
						})
					}
					name := strings.ToUpper(typ)
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, integerRangeSelect(backend, "SELECT v FROM r", []string{"v"}, []string{name}, nil, []bool{true}))
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO r VALUES ('[7,8)')", 1))
					atomic := "INSERT INTO r VALUES ('[1,4]'),(" + integerRangeLiteral(tc.input) + ")"
					want.Position = strconv.Itoa(utf8.RuneCountInString(atomic[:strings.LastIndex(atomic, "('")+1]) + 1)
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(atomic, want)
						s.Prepared, s.ErrorStage = prepared, "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					}
					want.Position, want.Context = "", `unnamed portal parameter $1 = '...'`
					if tc.input == "" {
						want.Context = `unnamed portal parameter $1 = ''`
					}
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO r VALUES ('[1,4]'),($1)", want, tc.input)))
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, integerRangeSelect(backend, "SELECT v FROM r", []string{"v"}, []string{name}, [][]any{{"[7,8)"}}, []bool{true}))
				}
			})
		}
	}
}

func TestReferenceIntegerRangeModifiers(t *testing.T) {
	for _, typ := range []string{"int4range", "int8range"} {
		t.Run(typ, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				for _, prepared := range []bool{false, true} {
					want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `type modifier is not allowed for type "` + typ + `"`, Position: "18"}
					s := fixture.ErrorDiagnostic("CREATE TABLE m(v "+typ+"(2))", want)
					s.Prepared, s.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				}
			}
		})
	}
}
