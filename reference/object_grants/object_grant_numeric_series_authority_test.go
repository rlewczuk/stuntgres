package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Released declarations are independent: omitted step is not a defaulted third
// formal, and a C implementation helper is not another SQL EXECUTE dependency.
func TestReferenceObjectGrantNumericSeriesAuthority(t *testing.T) {
	for _, long := range []bool{false, true} {
		name := "two_in"
		if long {
			name = "three_in"
		}
		t.Run(name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				formals := "numeric,numeric"
				args := []string{"0.10::numeric", "2.10::numeric"}
				values := [][]any{{"0.10"}, {"1.10"}, {"2.10"}}
				other := "generate_series(0.1::numeric,0.3::numeric,0.1::numeric)"
				otherRows := [][]any{{"0.1"}, {"0.2"}, {"0.3"}}
				if long {
					formals += ",numeric"
					args = []string{"0.1::numeric", "0.3::numeric", "0.1::numeric"}
					values, otherRows = otherRows, values
					other = "generate_series(0.10::numeric,2.10::numeric)"
				}
				target := "pg_catalog.generate_series(" + formals + ")"
				call := func(a []string) string { return "pg_catalog.generate_series(" + strings.Join(a, ",") + ")" }
				selected := call(args)
				selectRows := func(q string, rows [][]any, a ...any) fixture.Step {
					return fixture.Select(q, []string{"v"}, []string{"NUMERIC"}, rows, a...)
				}
				failure := func(q, code, message string, a ...any) fixture.Step {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message}, a...)
					s.SelectRows, s.ErrorStage = true, "execute"
					return s
				}
				run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO og_object_other", 0))
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_numeric_series_input(id integer,lo numeric)", 0))
				run(fixture.Exec("INSERT INTO og_numeric_series_input VALUES(1,0.1),(1,0.1)", 2))
				run(fixture.Exec("GRANT SELECT ON og_numeric_series_input TO og_object_reader", 0))
				// The owner retains X independently of the current reader. Both view
				// relation policies still invoke routines with the caller's authority.
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO og_object_other", 0))
				defer func() { run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM og_object_other", 0)) }()
				for _, view := range []struct{ name, option, body string }{
					{"og_numeric_series_target", "", "SELECT " + selected + " AS v"},
					{"og_numeric_series_source", " WITH(security_invoker=true)", "SELECT v FROM " + selected + " g(v)"},
				} {
					run(fixture.Exec("CREATE VIEW "+view.name+view.option+" AS "+view.body, 0))
					run(fixture.Exec("ALTER VIEW "+view.name+" OWNER TO og_object_other", 0))
					run(fixture.Exec("GRANT SELECT ON "+view.name+" TO og_object_reader", 0))
				}
				cases := []objectGrantProviderCase{
					{selectRows("SELECT "+selected+" AS v ORDER BY v", values), true, true},
					{selectRows("SELECT v FROM "+selected+" g(v) ORDER BY v", values), true, true},
					{selectRows("SELECT v FROM ROWS FROM("+selected+") g(v) ORDER BY v", values), true, true},
					{selectRows("SELECT v FROM og_numeric_series_target ORDER BY v", values), true, true},
					{selectRows("SELECT v FROM og_numeric_series_source ORDER BY v", values), true, true},
					// Executor initialization and body demand are separate hypotheses:
					// root pins these complete definitions against PostgreSQL first.
					{selectRows("SELECT "+selected+" AS v WHERE FALSE", nil), true, true},
					{selectRows("SELECT v FROM "+selected+" g(v) WHERE FALSE", nil), false, true},
					{selectRows("SELECT "+selected+" AS v LIMIT 0", nil), true, true},
					{selectRows("SELECT v FROM "+selected+" g(v) LIMIT 0", nil), true, true},
				}
				ordinalRows := make([][]any, len(values))
				for i, row := range values {
					ordinalRows[i] = []any{row[0], int64(i + 1)}
				}
				cases = append(cases, objectGrantProviderCase{fixture.Select("SELECT v,n FROM "+selected+" WITH ORDINALITY g(v,n) ORDER BY n", []string{"v", "n"}, []string{"NUMERIC", "INT8"}, ordinalRows), true, true})
				lateralArgs := []string{"lo", "1.1::numeric"}
				lateralRows := [][]any{{int64(1), "0.1", int64(1)}, {int64(1), "0.1", int64(1)}, {int64(1), "1.1", int64(2)}, {int64(1), "1.1", int64(2)}}
				if long {
					lateralArgs = []string{"lo", "0.2::numeric", "0.1::numeric"}
					lateralRows[2][1], lateralRows[3][1] = "0.2", "0.2"
				}
				lateralArgs[0] = "CASE WHEN id=1 THEN $1::numeric ELSE lo END"
				cases = append(cases, objectGrantProviderCase{fixture.Select("SELECT id,v,n FROM og_numeric_series_input CROSS JOIN LATERAL "+call(lateralArgs)+" WITH ORDINALITY g(v,n) ORDER BY id,v,n", []string{"id", "v", "n"}, []string{"INT4", "NUMERIC", "INT8"}, lateralRows, "0.1"), true, true})
				for i := range args {
					nulls := append([]string(nil), args...)
					nulls[i] = "NULL::numeric"
					cases = append(cases,
						objectGrantProviderCase{selectRows("SELECT "+call(nulls)+" AS v", nil), true, true},
						objectGrantProviderCase{selectRows("SELECT v FROM "+call(nulls)+" g(v)", nil), true, true})
					nulls[i] = "$1::numeric"
					cases = append(cases,
						objectGrantProviderCase{selectRows("SELECT "+call(nulls)+" AS v", nil, nil), true, true},
						objectGrantProviderCase{selectRows("SELECT v FROM "+call(nulls)+" g(v)", nil, nil), true, true})
				}
				wrong := append([]string(nil), args...)
				wrong[0], wrong[1] = "3::numeric", "1::numeric"
				cases = append(cases, objectGrantProviderCase{selectRows("SELECT v FROM "+call(wrong)+" g(v)", nil), true, true})
				bad := append([]string(nil), args...)
				bad[0] = "'NaN'::numeric"
				q := "SELECT v FROM " + call(bad) + " g(v)"
				cases = append(cases, objectGrantProviderCase{failure(q, "22023", "start value cannot be NaN"), true, true})
				if long {
					bad = append([]string(nil), args...)
					bad[2] = "0::numeric"
					q = "SELECT v FROM " + call(bad) + " g(v)"
					cases = append(cases,
						objectGrantProviderCase{failure(q, "22023", "step size cannot equal zero"), true, true},
						objectGrantProviderCase{selectRows(q+" LIMIT 0", nil), true, true},
						objectGrantProviderCase{selectRows("SELECT "+call(bad)+" AS v LIMIT 0", nil), true, true})
				}
				parameterArgs := append([]string(nil), args...)
				parameterArgs[0] = "$1::numeric"
				q = "SELECT v FROM " + call(parameterArgs) + " g(v) ORDER BY v"
				cases = append(cases, objectGrantProviderCase{selectRows(q, values, strings.TrimSuffix(args[0], "::numeric")), true, true})
				importFailure := failure(q, "22P02", `invalid input syntax for type numeric: "oops"`, "oops")
				importFailure.Diagnostic.Context = "unnamed portal parameter $1 = '...'"
				cases = append(cases, objectGrantProviderCase{importFailure, false, true})
				controls := []fixture.Step{
					selectRows("SELECT "+other+" AS v ORDER BY v", otherRows),
					fixture.Select("SELECT generate_series(1,2) AS v ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}),
				}
				objectGrantProviderLifecycle(t, p, b, c, run, prepared, target, "function", "generate_series", cases, controls)

				// INSERT SELECT uses the same physical warmed statement through all
				// ACL phases; denial publishes neither a result nor a partial prefix.
				defer objectGrantRoutineCleanup(t, p, c, []string{target})
				run(fixture.Exec("CREATE TABLE og_numeric_series_sink(v numeric)", 0))
				run(fixture.Exec("INSERT INTO og_numeric_series_sink VALUES(99)", 1))
				run(fixture.Exec("GRANT SELECT,INSERT,DELETE ON og_numeric_series_sink TO og_object_reader", 0))
				insert := "INSERT INTO og_numeric_series_sink SELECT " + selected
				stmt, err := c.PrepareContext(p.Ctx, insert)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: insert}
				for phase, allowed := range []bool{true, false, true, false} {
					switch phase {
					case 1:
						run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM PUBLIC", 0))
					case 2:
						run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO og_object_reader", 0))
					case 3:
						run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM og_object_reader", 0))
					}
					fixture.RoleGrantActor(run, "og_object_reader", func() {
						for _, retained := range []bool{false, true} {
							s := fixture.Exec(insert, 3)
							if !allowed {
								s = fixture.ImplicitError(insert, fixture.Diagnostic{Code: "42501", Message: "permission denied for function generate_series"})
								s.ErrorStage = "execute"
							}
							if retained {
								fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
							} else {
								run(s)
							}
							want := [][]any{{"99"}}
							if allowed {
								want = append(append([][]any(nil), values...), []any{"99"})
							}
							run(selectRows("SELECT v FROM og_numeric_series_sink ORDER BY v", want))
							if allowed {
								run(fixture.Exec("DELETE FROM og_numeric_series_sink WHERE v<>99", 3))
							}
						}
					})
				}
			})
		})
	}
}
