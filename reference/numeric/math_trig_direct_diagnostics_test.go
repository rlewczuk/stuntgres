package reference_test

import (
	"database/sql"
	"math"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceMathTrigDirectDomainDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		for _, name := range referenceMathTrigDirectNames {
			for _, input := range []string{"Infinity", "-Infinity"} {
				for _, prefix := range []string{"", "/*é*/ "} {
					t.Run(name+input+prefix, func(t *testing.T) {
						// Prepared RunStep proves Prepare succeeds, Execute alone fails,
						// and no cursor is returned. Full optional logical fields are absent.
						referenceMathTrigStep(t, p, backend, referenceMathTrigDomain(prefix+"SELECT "+name+"('"+input+"')"))
					})
				}
			}
		}
	})
}

func TestReferenceMathTrigDirectAnalysisDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	type analysisCase struct{ query, code, message, position, hint string }
	var cases []analysisCase
	for _, name := range referenceMathTrigDirectNames {
		for _, prefix := range []string{"", "/*é*/ "} {
			cases = append(cases, analysisCase{prefix + "SELECT " + name + "('oops')", "22P02", `invalid input syntax for type double precision: "oops"`, strconv.Itoa(9 + len(name) + len([]rune(prefix))), ""})
		}
		for _, tc := range []struct{ spelling, args, signature string }{
			{name, "", ""}, {name, "1,1", "integer, integer"},
			{name, "TRUE", "boolean"}, {name, "'0'::text", "text"},
			{name, "NULL::text", "text"}, {name, "x => 0", "x => integer"},
			{name, "VARIADIC ARRAY[1]", "integer[]"},
			{"pg_catalog." + name, "TRUE", "boolean"},
			{"public." + name, "1", "integer"},
			{`"` + strings.ToUpper(name) + `"`, "1", "integer"},
		} {
			messageName := strings.ReplaceAll(tc.spelling, `"`, "")
			cases = append(cases, analysisCase{"SELECT " + tc.spelling + "(" + tc.args + ")", "42883", "function " + messageName + "(" + tc.signature + ") does not exist", "8", hint})
		}
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

func TestReferenceMathTrigDirectHeldParameters(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		for _, name := range referenceMathTrigDirectNames {
			t.Run(name, func(t *testing.T) {
				query := "SELECT " + name + "($1)"
				stmt, err := backend.DB.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{backend.DB, stmt, query}
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, held, backend.Recorder, s) }
				input, want := int64(0), float64(0)
				switch name {
				case "cos", "cosd":
					want = 1
				case "cot", "cotd", "tan", "tand":
					// Avoid platform-sensitive non-special equality in the generic fixture.
					if name == "cot" || name == "tan" {
						input, want = 0, 0
						if name == "cot" {
							want = math.Inf(1)
						}
					} else {
						input, want = 45, 1
					}
				case "sind":
					input, want = 30, .5
				}
				for _, value := range []any{input, strconv.FormatInt(input, 10), float64(input), nil} {
					var expected any = want
					if value == nil {
						expected = nil
					}
					step := fixture.Select(query, []string{name}, []string{"FLOAT8"}, [][]any{{expected}}, value)
					step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { referenceMathTrigMetadata(t, rows, backend.PG, false) }
					run(step)
				}
				failure := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type double precision: "oops"`, Context: "unnamed portal parameter $1 = '...'"}, "oops")
				failure.SelectRows, failure.ErrorStage = true, "execute"
				run(failure)
				for _, value := range []any{"Infinity", "-Infinity"} {
					failure = referenceMathTrigDomain(query)
					failure.Args = []any{value}
					run(failure)
					run(fixture.Select(query, []string{name}, []string{"FLOAT8"}, [][]any{{want}}, input))
				}
				if name == "sind" {
					run(fixture.Select(query, []string{name}, []string{"FLOAT8"}, [][]any{{float64(-.5)}}, "-30"))
					run(fixture.Select(query, []string{name}, []string{"FLOAT8"}, [][]any{{nil}}, nil))
					run(fixture.Select(query, []string{name}, []string{"FLOAT8"}, [][]any{{float64(.5)}}, 30))
				}
			})
		}
	})
}

func TestReferenceMathTrigDirectSQLPrepared(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		conn, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		for _, name := range referenceMathTrigDirectNames {
			t.Run(name, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
				run(fixture.Exec("PREPARE d AS SELECT "+name+"($1)", 0))
				input, want := "0", float64(0)
				if name == "cos" || name == "cosd" {
					want = 1
				}
				if name == "cot" || name == "cotd" {
					input, want = "45", 1
				}
				if name == "cot" {
					input, want = "'0'", math.Inf(1)
				}
				if name == "sind" {
					input, want = "30", .5
				}
				if name == "tand" {
					input, want = "45", 1
				}
				for _, actual := range []string{input, "NULL", input} {
					var expected any = want
					if actual == "NULL" {
						expected = nil
					}
					step := fixture.Select("EXECUTE d("+actual+")", []string{name}, []string{"FLOAT8"}, [][]any{{expected}})
					step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { referenceMathTrigMetadata(t, rows, backend.PG, false) }
					run(step)
				}
				for _, input := range []string{"Infinity", "-Infinity"} {
					run(referenceMathTrigDomain("EXECUTE d('" + input + "')"))
				}
				// SQL EXECUTE input conversion has SQL source Position, not wire
				// unnamed-portal Context, as independently observed on pinned PG18.
				bad := fixture.ImplicitError("EXECUTE d('oops')", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type double precision: "oops"`, Position: "11"})
				bad.SelectRows, bad.ErrorStage = true, "execute"
				run(bad)
				run(fixture.Select("EXECUTE d("+input+")", []string{name}, []string{"FLOAT8"}, [][]any{{want}}))
				run(fixture.Exec("DEALLOCATE d", 0))
				run(fixture.Exec("PREPARE d AS SELECT "+name+"('Infinity')", 0))
				run(referenceMathTrigDomain("EXECUTE d"))
				run(fixture.Exec("DEALLOCATE d", 0))
			})
		}
	})
}

func TestReferenceMathTrigDirectNullDemand(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		run(fixture.Exec("CREATE TABLE direct_demand(id INT,n DOUBLE PRECISION,d DOUBLE PRECISION)", 0))
		for _, name := range referenceMathTrigDirectNames {
			referenceMathTrigStep(t, p, backend, referenceMathTrigDomain("SELECT "+name+"('Infinity') FROM direct_demand"))
			step := fixture.Select("SELECT "+name+"(d) FROM direct_demand", []string{name}, []string{"FLOAT8"}, nil)
			step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { referenceMathTrigMetadata(t, rows, backend.PG, false) }
			referenceMathTrigStep(t, p, backend, step)
		}
		run(fixture.Exec("INSERT INTO direct_demand VALUES(1,NULL,'Infinity')", 1))
		referenceMathTrigStep(t, p, backend, fixture.Select("SELECT atan2(NULL,sin(d)) FROM direct_demand", []string{"atan2"}, []string{"FLOAT8"}, [][]any{{nil}}))
		referenceMathTrigStep(t, p, backend, referenceMathTrigDomain("SELECT atan2(n,sin(d)) FROM direct_demand"))
		referenceMathTrigStep(t, p, backend, referenceMathTrigDomain("SELECT atan2(NULL,sin('Infinity'))"))
	})
}
