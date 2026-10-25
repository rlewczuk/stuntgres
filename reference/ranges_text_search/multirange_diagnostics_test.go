package reference_test

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func multirangeFailures(typ string) []integerRangeFailure {
	cases := []integerRangeFailure{}
	for _, tc := range []struct{ input, detail string }{
		{"", "Missing left brace."}, {"empty", "Missing left brace."}, {"[1,4)", "Missing left brace."},
		{"{", "Unexpected end of input."}, {"{[1,4)", "Unexpected end of input."}, {"{[1,4}", "Unexpected end of input."},
		{`{["1,4)}`, "Unexpected end of input."}, {`{[1,4\`, "Unexpected end of input."},
		{"{,}", "Expected range start."}, {"{NULL}", "Expected range start."}, {"{{[1,4)}}", "Expected range start."},
		{`{"[1,4)"}`, "Expected range start."}, {"{[1,4),}", "Expected range start."}, {"{empty,}", "Expected range start."},
		{"{emptyx}", "Expected comma or end of multirange."}, {"{[1,4) [8,9)}", "Expected comma or end of multirange."},
		{"{[1,4);[8,9)}", "Expected comma or end of multirange."}, {"{}junk", "Junk after closing right brace."},
		{"{[1,4)}junk", "Junk after closing right brace."}, {"{[oops,4", "Unexpected end of input."},
	} {
		cases = append(cases, integerRangeFailure{tc.input, "22P02", `malformed multirange literal: "` + tc.input + `"`, tc.detail})
	}
	for _, tc := range []struct{ input, member, detail string }{
		{"{[1)}", "[1)", "Missing comma after lower bound."},
		{"{[1,2,3]}", "[1,2,3]", "Too many commas."},
	} {
		cases = append(cases, integerRangeFailure{tc.input, "22P02", `malformed range literal: "` + tc.member + `"`, tc.detail})
	}
	subtype, max, min, over := "integer", "2147483647", "-2147483648", "2147483648"
	if typ == "int8multirange" {
		subtype, max, min, over = "bigint", "9223372036854775807", "-9223372036854775808", "9223372036854775808"
	}
	if typ == "nummultirange" {
		subtype = "numeric"
	}
	for _, input := range []string{"{[oops,4)}", "{[oops,4)}junk"} {
		cases = append(cases, integerRangeFailure{input, "22P02", `invalid input syntax for type ` + subtype + `: "oops"`, ""})
	}
	for _, bound := range []struct{ input, decoded string }{
		{`{["1,2",4)}`, "1,2"},
		{`{[1\,2,4)}`, "1,2"},
		{`{["1""2",4)}`, `1"2`},
		{`{["1\"2",4)}`, `1"2`},
	} {
		cases = append(cases, integerRangeFailure{bound.input, "22P02", `invalid input syntax for type ` + subtype + `: "` + bound.decoded + `"`, ""})
	}
	for _, input := range []string{"{[4,1)}", "{[4,1),[oops,4)}"} {
		cases = append(cases, integerRangeFailure{input, "22000", "range lower bound must be less than or equal to range upper bound", ""})
	}
	if typ != "nummultirange" {
		cases = append(cases, integerRangeFailure{"{[1," + over + ")}", "22003", `value "` + over + `" is out of range for type ` + subtype, ""})
		for _, member := range []string{"[1," + max + "]", "[" + max + "," + max + "]", "(" + max + ",)"} {
			cases = append(cases, integerRangeFailure{"{" + member + "}", "22003", subtype + " out of range", ""})
		}
		cases = append(cases, integerRangeFailure{"{[" + max + "," + min + "]}", "22000", "range lower bound must be less than or equal to range upper bound", ""})
	}
	if typ == "nummultirange" {
		for _, member := range []string{"[1e131072,2)", "[0,1e-16384)", "[1e131072,oops]"} {
			cases = append(cases, integerRangeFailure{"{" + member + "}", "22003", "value overflows numeric format", ""})
		}
	}
	return cases
}

func TestReferenceMultirangeDiagnostics(t *testing.T) {
	for _, typ := range []string{"int4multirange", "int8multirange", "nummultirange"} {
		for i, tc := range multirangeFailures(typ) {
			t.Run(fmt.Sprintf("%s/%02d", typ, i), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE r(v "+typ+")", 0))
					want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Detail: tc.detail, Position: "29"}
					query := "/*é*/ INSERT INTO r VALUES (" + rangeLiteral(tc.input) + ")"
					for _, route := range []string{"exec", "prepare", "query", "bind", "prepared_bind"} {
						d := want
						s := fixture.ErrorDiagnostic(query, d)
						s.ErrorStage = "execute"
						switch route {
						case "prepare":
							s.Prepared, s.ErrorStage = true, "prepare"
						case "query":
							s.SelectRows = true
						case "bind", "prepared_bind":
							d.Position, d.Context = "", `unnamed portal parameter $1 = '...'`
							if tc.input == "" {
								d.Context = `unnamed portal parameter $1 = ''`
							}
							s = fixture.ErrorDiagnostic("INSERT INTO r VALUES ($1)", d, tc.input)
							s.Prepared, s.ErrorStage = route == "prepared_bind", "execute"
						}
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					}
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO r VALUES ('{[7,8)}')", 1))
					atomic := "INSERT INTO r VALUES ('{[1,4]}'),(" + rangeLiteral(tc.input) + ")"
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
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO r VALUES ('{[1,4]}'),($1)", want, tc.input)))
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, rangeSelect(backend, "SELECT v FROM r", []string{"v"}, []string{strings.ToUpper(typ)}, [][]any{{"{[7,8)}"}}, []bool{true}))
				}
			})
		}
	}
}

func TestReferenceMultirangeModifiers(t *testing.T) {
	for _, typ := range []string{"int4multirange", "int8multirange", "nummultirange"} {
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
