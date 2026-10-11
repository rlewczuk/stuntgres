package reference_test

import (
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func referenceMathConstantsParameterBaseline(name string) (int64, float64) {
	if name == "gamma" {
		return 1, 1
	}
	if name == "erfc" || name == "exp" {
		return 0, 1
	}
	return 0, 0
}

func TestReferenceMathConstantsHeldParameters(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		conn, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		for _, name := range referenceMathConstantsUnaryNames {
			t.Run(name, func(t *testing.T) {
				query := "/*é*/ SELECT " + name + "($1),pi() AS p"
				stmt, err := conn.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{conn, stmt, query}
				input, want := referenceMathConstantsParameterBaseline(name)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, held, backend.Recorder, s) }
				valid := fixture.Select(query, []string{name, "p"}, []string{"FLOAT8", "FLOAT8"}, [][]any{{want, 3.141592653589793}}, input)
				for _, arg := range []any{input, float64(input), strconv.FormatInt(input, 10), nil} {
					s := valid
					s.Args = []any{arg}
					if arg == nil {
						s.Rows = [][]any{{nil, 3.141592653589793}}
					}
					run(s)
				}
				bad := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type double precision: "oops"`, Context: "unnamed portal parameter $1 = '...'"}, "oops")
				bad.SelectRows, bad.ErrorStage = true, "execute"
				run(bad)
				run(valid)
				category, arg := "", ""
				switch name {
				case "gamma":
					category, arg = "overflow", "0"
				case "degrees":
					category, arg = "overflow", "1e308"
				case "radians":
					category, arg = "underflow", "5e-324"
				case "exp":
					category, arg = "underflow", "-746"
				}
				if category != "" {
					bad = fixture.ImplicitError(query, fixture.Diagnostic{Code: "22003", Message: "value out of range: " + category}, arg)
					bad.SelectRows, bad.ErrorStage = true, "execute"
					run(bad)
					run(valid)
				}
			})
		}
	})
}

func TestReferenceMathConstantsSQLPrepared(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		conn, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
		for _, name := range referenceMathConstantsUnaryNames {
			t.Run(name, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
				run(fixture.Exec("/*é*/ PREPARE constants_parameter AS SELECT "+name+"($1),pi() AS p", 0))
				input, want := referenceMathConstantsParameterBaseline(name)
				valid := fixture.Select("EXECUTE constants_parameter('"+strconv.FormatInt(input, 10)+"')", []string{name, "p"}, []string{"FLOAT8", "FLOAT8"}, [][]any{{want, 3.141592653589793}})
				run(valid)
				run(fixture.Select("EXECUTE constants_parameter(NULL)", []string{name, "p"}, []string{"FLOAT8", "FLOAT8"}, [][]any{{nil, 3.141592653589793}}))
				q := "/*é*/ EXECUTE constants_parameter('oops')"
				bad := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type double precision: "oops"`, Position: strconv.Itoa(len([]rune("/*é*/ EXECUTE constants_parameter(")) + 1)})
				bad.SelectRows, bad.ErrorStage = true, "execute"
				run(bad)
				run(valid)
				run(fixture.Exec("DEALLOCATE constants_parameter", 0))
			})
		}
		run(fixture.Exec("PREPARE constants_pi AS SELECT pi() AS p", 0))
		run(fixture.Select("EXECUTE constants_pi", []string{"p"}, []string{"FLOAT8"}, [][]any{{3.141592653589793}}))
		run(fixture.Exec("DEALLOCATE constants_pi", 0))
	})
}
