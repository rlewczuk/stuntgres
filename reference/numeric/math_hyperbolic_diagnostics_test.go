package reference_test

import (
	"math"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func referenceHyperbolicDomain(query string) fixture.Step {
	s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22003", Message: "input is out of range"})
	s.SelectRows, s.ErrorStage = true, "execute"
	return s
}

func TestReferenceMathHyperbolicDomainDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceHyperbolicBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		for _, tc := range []struct {
			name   string
			inputs []string
		}{
			{"acosh", []string{"-Infinity", "-1", "-0", "0", "0.9999999999999999"}},
			{"atanh", []string{"-Infinity", "Infinity", "-2", "2", "-1.0000000000000002", "1.0000000000000002", "1.7976931348623157e308", "-1.7976931348623157e308"}},
		} {
			for _, input := range tc.inputs {
				t.Run(tc.name+"_"+input, func(t *testing.T) {
					referenceMathTrigStep(t, p, backend, referenceHyperbolicDomain("SELECT "+tc.name+"('"+input+"')"))
				})
			}
		}
		referenceMathTrigStep(t, p, backend, referenceHyperbolicDomain("/*é*/ SELECT acosh(0)"))
	})
}

func TestReferenceMathHyperbolicAnalysisDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	cases := []struct{ query, code, message, position, hint string }{}
	for _, name := range referenceHyperbolicNames {
		for _, args := range []struct{ sql, signature string }{{"", ""}, {"1,1", "integer, integer"}, {"TRUE", "boolean"}, {"'0'::text", "text"}} {
			cases = append(cases, struct{ query, code, message, position, hint string }{"SELECT " + name + "(" + args.sql + ")", "42883", "function " + name + "(" + args.signature + ") does not exist", "8", hint})
		}
		for _, spelling := range []string{`"` + strings.ToUpper(name) + `"`, "public." + name, "pg_catalog." + name} {
			arg, signature := "1", "integer"
			if spelling == "pg_catalog."+name {
				arg, signature = "TRUE", "boolean"
			}
			cases = append(cases, struct{ query, code, message, position, hint string }{"SELECT " + spelling + "(" + arg + ")", "42883", "function " + strings.ReplaceAll(spelling, `"`, "") + "(" + signature + ") does not exist", "8", hint})
		}
		for _, prefix := range []string{"", "/*é*/ "} {
			// PostgreSQL positions count characters, not UTF8 bytes.
			position := strconv.Itoa(9 + len(name) + len([]rune(prefix)))
			cases = append(cases, struct{ query, code, message, position, hint string }{prefix + "SELECT " + name + "('oops')", "22P02", `invalid input syntax for type double precision: "oops"`, position, ""})
		}
	}
	referenceHyperbolicBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		for _, tc := range cases {
			t.Run(tc.query, func(t *testing.T) {
				s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position, Hint: tc.hint})
				s.SelectRows, s.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				s.ErrorStage = "prepare"
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(s))
			})
		}
	})
}

func TestReferenceMathHyperbolicHeldParameters(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceHyperbolicBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		const query = "SELECT atanh($1)"
		stmt, err := backend.DB.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{backend.DB, stmt, query}
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, held, backend.Recorder, s) }
		value := func(input, want any) {
			run(fixture.Select(query, []string{"atanh"}, []string{"FLOAT8"}, [][]any{{want}}, input))
		}
		value(0, float64(0))
		value("0.5", 0.5493061443340548)
		bad := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type double precision: "oops"`, Context: "unnamed portal parameter $1 = '...'"}, "oops")
		bad.SelectRows, bad.ErrorStage = true, "execute"
		run(bad)
		domain := referenceHyperbolicDomain(query)
		domain.Args = []any{2}
		run(domain)
		value(nil, nil)
		value("-0.5", -0.5493061443340548)
		value(1, math.Inf(1))
		value(0, float64(0))
	})
}
