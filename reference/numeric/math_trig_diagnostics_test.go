package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func referenceMathTrigDomain(query string) fixture.Step {
	s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22003", Message: "input is out of range"})
	s.SelectRows, s.ErrorStage = true, "execute"
	return s
}

func TestReferenceMathTrigDomainDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		for _, name := range []string{"acos", "acosd", "asin", "asind"} {
			for _, input := range []string{"2", "-2", "Infinity", "-Infinity", "1.0000000000000002", "-1.0000000000000002"} {
				t.Run(name+"_"+input, func(t *testing.T) {
					referenceMathTrigStep(t, p, backend, referenceMathTrigDomain("SELECT "+name+"('"+input+"')"))
				})
			}
		}
		// A Unicode prefix cannot invent an execution-time source location.
		referenceMathTrigStep(t, p, backend, referenceMathTrigDomain("/*é*/ SELECT acos(2)"))
	})
}

func TestReferenceMathTrigAnalysisDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	cases := []struct{ query, code, message, position, hint string }{
		{"SELECT acos('oops')", "22P02", `invalid input syntax for type double precision: "oops"`, "13", ""},
		{"SELECT acosd('oops')", "22P02", `invalid input syntax for type double precision: "oops"`, "14", ""},
		{"SELECT asin('oops')", "22P02", `invalid input syntax for type double precision: "oops"`, "13", ""},
		{"SELECT asind('oops')", "22P02", `invalid input syntax for type double precision: "oops"`, "14", ""},
		{"SELECT atan('oops')", "22P02", `invalid input syntax for type double precision: "oops"`, "13", ""},
		{"SELECT atand('oops')", "22P02", `invalid input syntax for type double precision: "oops"`, "14", ""},
		{"SELECT atan2('oops',0)", "22P02", `invalid input syntax for type double precision: "oops"`, "14", ""},
		{"SELECT atan2d('oops',0)", "22P02", `invalid input syntax for type double precision: "oops"`, "15", ""},
		{"/*é*/ SELECT acos('oops')", "22P02", `invalid input syntax for type double precision: "oops"`, "19", ""},
		{"SELECT atan2(NULL,'oops')", "22P02", `invalid input syntax for type double precision: "oops"`, "19", ""},
		{"SELECT acos(TRUE)", "42883", "function acos(boolean) does not exist", "8", hint},
		{"SELECT pg_catalog.acos(TRUE)", "42883", "function pg_catalog.acos(boolean) does not exist", "8", hint},
		{`SELECT "ACOS"(1)`, "42883", "function ACOS(integer) does not exist", "8", hint},
		{"SELECT public.acos(1)", "42883", "function public.acos(integer) does not exist", "8", hint},
		{"SELECT acos('0'::text)", "42883", "function acos(text) does not exist", "8", hint},
		{"SELECT atan2(1,'0'::text)", "42883", "function atan2(integer, text) does not exist", "8", hint},
		{"SELECT acos(x => 0)", "42883", "function acos(x => integer) does not exist", "8", hint},
		{"SELECT atan2(y => 1,x => 1)", "42883", "function atan2(y => integer, x => integer) does not exist", "8", hint},
		{"SELECT acos(VARIADIC ARRAY[1])", "42883", "function acos(integer[]) does not exist", "8", hint},
	}
	for _, name := range []string{"acos", "acosd", "asin", "asind", "atan", "atand", "atan2", "atan2d"} {
		cases = append(cases, struct{ query, code, message, position, hint string }{"SELECT " + name + "()", "42883", "function " + name + "() does not exist", "8", hint})
		args, signature := "1,1", "integer, integer"
		if name == "atan2" || name == "atan2d" {
			args, signature = "1", "integer"
		}
		cases = append(cases, struct{ query, code, message, position, hint string }{"SELECT " + name + "(" + args + ")", "42883", "function " + name + "(" + signature + ") does not exist", "8", hint})
	}
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		for _, tc := range cases {
			t.Run(tc.query, func(t *testing.T) {
				step := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position, Hint: tc.hint})
				step.SelectRows, step.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
				step.ErrorStage = "prepare"
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(step))
			})
		}
	})
}

func TestReferenceMathTrigHeldParameters(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		for _, tc := range []struct {
			query, name string
			binary      bool
		}{
			{"SELECT asind($1)", "asind", false}, {"SELECT atan2d($1,$2)", "atan2d", true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				stmt, err := backend.DB.PrepareContext(p.Ctx, tc.query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{backend.DB, stmt, tc.query}
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, held, backend.Recorder, s) }
				for _, input := range []any{float64(0.5), "0.5", int64(1), nil, "-0.5"} {
					args, want := []any{input}, any(float64(30))
					if input == int64(1) {
						want = float64(90)
					}
					if input == nil {
						want = nil
					}
					if input == "-0.5" {
						want = float64(-30)
					}
					if tc.binary {
						args = append(args, input)
						want = float64(45)
						if input == nil {
							want = nil
						}
						if input == "-0.5" {
							want = float64(-135)
						}
					}
					step := fixture.Select(tc.query, []string{tc.name}, []string{"FLOAT8"}, [][]any{{want}}, args...)
					run(step)
				}
				for _, slot := range []int{0, 1} {
					if slot == 1 && !tc.binary {
						continue
					}
					args := []any{"oops"}
					if tc.binary {
						args = []any{1, 1}
						args[slot] = "oops"
					}
					context := "unnamed portal parameter $1 = '...'"
					if slot == 1 {
						context = "unnamed portal parameter $2 = '...'"
					}
					failure := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type double precision: "oops"`, Context: context}, args...)
					failure.SelectRows, failure.ErrorStage = true, "execute"
					run(failure)
					args, want := []any{float64(0.5)}, float64(30)
					if tc.binary {
						args, want = []any{1, 1}, 45
					}
					run(fixture.Select(tc.query, []string{tc.name}, []string{"FLOAT8"}, [][]any{{want}}, args...))
				}
				if !tc.binary {
					failure := referenceMathTrigDomain(tc.query)
					failure.Args = []any{2}
					run(failure)
					run(fixture.Select(tc.query, []string{tc.name}, []string{"FLOAT8"}, [][]any{{nil}}, nil))
					run(fixture.Select(tc.query, []string{tc.name}, []string{"FLOAT8"}, [][]any{{float64(-30)}}, "-0.5"))
				}
			})
		}
	})
}

func TestReferenceMathTrigNullDemand(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		for _, name := range []string{"acos", "acosd", "asin", "asind", "atan", "atand"} {
			referenceMathTrigStep(t, p, backend, fixture.Select("SELECT "+name+"(NULL)", []string{name}, []string{"FLOAT8"}, [][]any{{nil}}))
		}
		for _, name := range []string{"atan2", "atan2d"} {
			for _, args := range []string{"NULL,1", "1,NULL", "NULL,NULL", "NULL,'NaN'", "'NaN',NULL", "NULL,'Infinity'", "'-Infinity',NULL"} {
				referenceMathTrigStep(t, p, backend, fixture.Select("SELECT "+name+"("+args+")", []string{name}, []string{"FLOAT8"}, [][]any{{nil}}))
			}
		}
		run(fixture.Exec("CREATE TABLE trig_demand(id INT,n DOUBLE PRECISION,d DOUBLE PRECISION)", 0))
		referenceMathTrigStep(t, p, backend, referenceMathTrigDomain("SELECT acos(2) FROM trig_demand"))
		empty := fixture.Select("SELECT asin(d) FROM trig_demand", []string{"asin"}, []string{"FLOAT8"}, nil)
		referenceMathTrigStep(t, p, backend, empty)
		run(fixture.Exec("INSERT INTO trig_demand VALUES(1,NULL,2)", 1))
		referenceMathTrigStep(t, p, backend, fixture.Select("SELECT atan2(NULL,asin(d)) FROM trig_demand", []string{"atan2"}, []string{"FLOAT8"}, [][]any{{nil}}))
		referenceMathTrigStep(t, p, backend, referenceMathTrigDomain("SELECT atan2(n,asin(d)) FROM trig_demand"))
		referenceMathTrigStep(t, p, backend, referenceMathTrigDomain("SELECT atan2(NULL,asin(2))"))
	})
}
