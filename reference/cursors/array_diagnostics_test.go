package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

type arrayReferenceBadInput struct{ input, typ, code, message, detail string }

func arrayReferenceBadInputs() []arrayReferenceBadInput {
	cases := []arrayReferenceBadInput{}
	for _, tc := range []struct{ input, detail string }{
		{"", `Array value must start with "{" or dimension information.`},
		{"x", `Array value must start with "{" or dimension information.`}, {"}{", `Array value must start with "{" or dimension information.`},
		{"[1:2]{1,2}", `Missing "=" after array dimensions.`}, {"[1:2]=x", `Array contents must start with "{".`},
		{"[:1]={1}", `"[" must introduce explicitly-specified array dimensions.`}, {"[ 1:2]={1,2}", `"[" must introduce explicitly-specified array dimensions.`},
		{"[1:]={1}", "Missing array dimension value."}, {"[1:2={1,2}", `Missing "]" after array dimensions.`}, {"[1 :2]={1,2}", `Missing "]" after array dimensions.`},
		{"[2]={1}", "Specified array dimensions do not match array contents."}, {"[0:2]={1,2}", "Specified array dimensions do not match array contents."},
		{"{{1},{2,3}}", "Multidimensional arrays must have sub-arrays with matching dimensions."}, {"{{1},{{2}}}", "Multidimensional arrays must have sub-arrays with matching dimensions."}, {"{{},{1}}", "Multidimensional arrays must have sub-arrays with matching dimensions."},
		{"{1,,2}", `Unexpected "," character.`}, {"{1,}", `Unexpected "}" character.`},
		{"{foo{}}", `Unexpected "{" character.`}, {`{"foo"{}}`, `Unexpected "{" character.`},
		{`{"a"b}`, "Incorrectly quoted array element."}, {`{a"b"}`, "Incorrectly quoted array element."}, {`{"a""b"}`, "Incorrectly quoted array element."},
		{`{{"1 2"} x,{3}}`, "Unexpected array element."}, {`{"unterminated}`, "Unexpected end of input."}, {`{a\}`, "Unexpected end of input."},
		{"{}}", "Junk after closing right brace."}, {"{1}x", "Junk after closing right brace."},
	} {
		cases = append(cases, arrayReferenceBadInput{tc.input, "text[]", "22P02", `malformed array literal: "` + tc.input + `"`, tc.detail})
	}
	cases = append(cases,
		arrayReferenceBadInput{"[1:0]={}", "int4[]", "2202E", "upper bound cannot be less than lower bound", ""},
		arrayReferenceBadInput{"[2147483646:2147483647]={1,2}", "int4[]", "54000", "array upper bound is too large: 2147483647", ""},
		arrayReferenceBadInput{"[2147483648:2147483648]={1}", "int4[]", "54000", "array bound is out of integer range", ""},
		arrayReferenceBadInput{"{{{{{{{1}}}}}}}", "int4[]", "54000", "number of array dimensions exceeds the maximum allowed (6)", ""},
		arrayReferenceBadInput{strings.Repeat("[1:1]", 7) + "={1}", "int4[]", "54000", "number of array dimensions exceeds the maximum allowed (6)", ""},
		arrayReferenceBadInput{"{1,oops}", "int4[]", "22P02", `invalid input syntax for type integer: "oops"`, ""},
		arrayReferenceBadInput{"{1,2147483648}", "int4[]", "22003", `value "2147483648" is out of range for type integer`, ""},
		arrayReferenceBadInput{"{truth}", "bool[]", "22P02", `invalid input syntax for type boolean: "truth"`, ""},
		arrayReferenceBadInput{`{"NULL"}`, "int4[]", "22P02", `invalid input syntax for type integer: "NULL"`, ""},
		arrayReferenceBadInput{"{oops,,2}", "int4[]", "22P02", `invalid input syntax for type integer: "oops"`, ""},
		arrayReferenceBadInput{"{{oops},{2,3}}", "int4[]", "22P02", `invalid input syntax for type integer: "oops"`, ""},
	)
	return cases
}

func TestReferenceArrayDiagnostics(t *testing.T) {
	for _, tc := range arrayReferenceBadInputs() {
		t.Run(tc.input+tc.typ, func(t *testing.T) {
			fixture.ArrayReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, prepared := range []bool{false, true} {
					for _, prefix := range []string{"", "/*é*/ "} {
						want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
						want.Detail = tc.detail
						want.Position = "8"
						if prefix != "" {
							want.Position = "14"
						}
						s := fixture.ErrorDiagnostic(prefix+"SELECT "+fixture.ArrayReferenceQuote(tc.input)+"::"+tc.typ+" AS a FROM public.one", want)
						s.SelectRows = true
						s.Prepared = prepared
						s.ErrorStage = "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
						run(s)
					}
					want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					want.Detail = tc.detail
					want.Context = "unnamed portal parameter $1 = '...'"
					if tc.input == "" {
						want.Context = "unnamed portal parameter $1 = ''"
					}
					s := fixture.ErrorDiagnostic("SELECT $1::"+tc.typ+" AS a FROM public.one", want, tc.input)
					s.SelectRows = true
					s.Prepared = prepared
					s.ErrorStage = "execute"
					run(s)
				}
				run(fixture.Exec("DELETE FROM public.one", 1))
				s := fixture.ErrorDiagnostic("SELECT "+fixture.ArrayReferenceQuote(tc.input)+"::"+tc.typ+" AS a FROM public.one", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Detail: tc.detail, Position: "8"})
				s.SelectRows = true
				s.Prepared = true
				s.ErrorStage = "prepare"
				run(s)
			})
		})
	}
}

func TestReferenceArrayAtomicity(t *testing.T) {
	for _, tc := range []arrayReferenceBadInput{
		{"{1,,2}", "int4[]", "22P02", `malformed array literal: "{1,,2}"`, `Unexpected "," character.`},
		{"{{1},{2,3}}", "int4[]", "22P02", `malformed array literal: "{{1},{2,3}}"`, "Multidimensional arrays must have sub-arrays with matching dimensions."},
		{"{oops}", "int4[]", "22P02", `invalid input syntax for type integer: "oops"`, ""},
	} {
		t.Run(tc.input, func(t *testing.T) {
			fixture.ArrayReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE public.array_atomic (id INT,a INT[])", 0))
				run(fixture.Exec("INSERT INTO public.array_atomic VALUES (1,'{42}')", 1))
				for _, prepared := range []bool{false, true} {
					want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					want.Detail = tc.detail
					want.Context = "unnamed portal parameter $2 = '...'"
					s := fixture.ErrorDiagnostic("INSERT INTO public.array_atomic VALUES (2,$1),(3,$2)", want, "{7}", tc.input)
					s.Prepared = prepared
					s.ErrorStage = "execute"
					run(s)
					run(fixture.ArrayReferenceSelect("SELECT id,a FROM public.array_atomic ORDER BY id", []string{"id", "a"}, []string{"INT4", "_INT4"}, []string{"INT4", "_INT4"}, [][]any{{int64(1), "{42}"}}, b.PG))
				}
			})
		})
	}
}
