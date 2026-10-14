package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRegtypeAliasModifierSyntax(t *testing.T) {
	fixture.RegtypeScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder, notices *[]fixture.Diagnostic) {
		const frameQuery = "SELECT $1::regtype AS r"
		const runtimeQuery = "SELECT $1::text::regtype AS r"
		frame, err := conn.PrepareContext(p.Ctx, frameQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer frame.Close()
		runtime, err := conn.PrepareContext(p.Ctx, runtimeQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer runtime.Close()
		for _, fact := range []struct{ input, primary, position string }{
			{"varchar('12')", `syntax error at or near "'12'"`, "9"},
			{"varchar((12))", `syntax error at or near "("`, "9"},
			// Source-grounded generic type/function-name admission; root must
			// confirm these standalone REGTYPE route records on the pinned oracle.
			{"NONE", `syntax error at or near "NONE"`, "1"},
		} {
			t.Run(fact.input, func(t *testing.T) {
				before := len(*notices)
				literalQuery := "SELECT /* é🙂 */ " + fixture.RegtypeLiteral(fact.input) + "::regtype AS r"
				for _, route := range []string{"literal-query", "literal-prepare", "direct-frame", "held-frame", "runtime-text", "held-runtime-text"} {
					t.Run(route, func(t *testing.T) {
						query := frameQuery
						var rows *sql.Rows
						var err error
						switch route {
						case "literal-query":
							query = literalQuery
							rows, err = conn.QueryContext(p.Ctx, query)
						case "literal-prepare":
							query = literalQuery
							statement, e := conn.PrepareContext(p.Ctx, query)
							err = e
							if statement != nil {
								statement.Close()
								t.Error("failed literal Prepare exposed statement")
							}
						case "direct-frame":
							rows, err = conn.QueryContext(p.Ctx, query, fact.input)
						case "held-frame":
							rows, err = frame.QueryContext(p.Ctx, fact.input)
						case "runtime-text":
							query = runtimeQuery
							rows, err = conn.QueryContext(p.Ctx, query, fact.input)
						case "held-runtime-text":
							query = runtimeQuery
							rows, err = runtime.QueryContext(p.Ctx, fact.input)
						}
						if rows != nil {
							rows.Close()
							t.Error("failed query exposed cursor")
						}
						fact := regtypeInvalidInput{input: fact.input, state: "42601", primary: fact.primary, position: fact.position, parseContext: true}
						want := regtypeInputDiagnostic(fact, route)
						fixture.AssertDiagnostic(t, err, &want)
					})
				}
				if len(*notices) != before {
					t.Errorf("invalid alias modifier emitted notices: %#v", (*notices)[before:])
				}
			})
		}
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec(`CREATE DOMAIN public."none" AS integer`, 0))
		for _, fact := range []struct{ input, output string }{
			{`"none"`, `"none"`},
			{"public.none", `"none"`},
			{`public."none"`, `"none"`},
			{"integer", "integer"},
			{"numeric(8,2)", "numeric"},
			{"character varying(12)", "character varying"},
			{"time", "time without time zone"},
		} {
			query := "SELECT " + fixture.RegtypeLiteral(fact.input) + "::regtype AS r"
			fixture.RegtypeBoth(t, p, conn, recorder, fixture.Select(query, []string{"r"}, []string{"REGTYPE"}, [][]any{{fact.output}}))
			for _, held := range []struct {
				query     string
				statement *sql.Stmt
			}{{frameQuery, frame}, {runtimeQuery, runtime}} {
				executor := fixture.HeldStatement{Executor: conn, Statement: held.statement, SQL: held.query}
				fixture.RegtypeRunStep(t, p.Ctx, executor, recorder, fixture.Select(held.query, []string{"r"}, []string{"REGTYPE"}, [][]any{{fact.output}}, fact.input))
			}
		}
	})
}
