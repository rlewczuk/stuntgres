package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantSoundexProviderAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, target, typ string
		value, empty      any
	}{
		{"soundex", "public.soundex(text)", "TEXT", "A500", ""},
		{"difference", "public.difference(text,text)", "INT4", int64(2), int64(4)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				// PostgreSQL owns a real database-wide installation; native
				// scenarios already have independent, fresh engines.
				if b.PG {
					clean := func() {
						for _, query := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "DROP EXTENSION IF EXISTS fuzzystrmatch CASCADE"} {
							if _, err := c.ExecContext(p.Ctx, query); err != nil {
								t.Error(query, err)
							}
						}
					}
					defer clean()
					clean()
				}
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE EXTENSION fuzzystrmatch SCHEMA public", 0))
				run(fixture.Exec("CREATE TABLE og_soundex_source(id INT,source TEXT,other TEXT)", 0))
				run(fixture.Exec("INSERT INTO og_soundex_source VALUES(1,'Anne','Andrew'),(2,NULL,'Andrew'),(3,'',''),(4,'Anne',NULL)", 4))
				run(fixture.Exec("GRANT SELECT ON og_soundex_source TO og_object_reader", 0))
				call := func(left, right string) string {
					if tc.name == "soundex" {
						return "public.soundex(" + left + ")"
					}
					return "public.difference(" + left + "," + right + ")"
				}
				selectValue := func(expr, tail string, value any, args ...any) fixture.Step {
					return fixture.Select("SELECT "+expr+" AS v FROM og_soundex_source"+tail, []string{"v"}, []string{tc.typ}, [][]any{{value}}, args...)
				}
				expr := call("source", "other")
				parameter := call("$1::TEXT", "other")
				run(fixture.Exec("CREATE VIEW og_soundex_view AS SELECT "+expr+" AS v FROM og_soundex_source WHERE id=1", 0))
				run(fixture.Exec("GRANT SELECT ON og_soundex_view TO og_object_reader", 0))
				cases := []objectGrantProviderCase{
					// Row-backed direct and stored consumers reuse these SAME
					// handles through PUBLIC revoke, actor grant, actor revoke.
					{selectValue(expr, " WHERE id=1", tc.value), true, true},
					{fixture.Select("SELECT v FROM og_soundex_view", []string{"v"}, []string{tc.typ}, [][]any{{tc.value}}), true, true},
					{selectValue(expr, " WHERE id=2", nil), true, true},
					{selectValue(expr, " WHERE id=3", tc.empty), true, true},
					{selectValue(parameter, " WHERE id=1", tc.value, "Anne"), true, true},
					// Strict literal and fresh frame NULL eliminate the call;
					// runtime row NULL above must still initialize authority.
					{selectValue(call("NULL::TEXT", "other"), " WHERE id=1", nil), false, true},
					{selectValue(parameter, " WHERE id=1", nil, nil), false, false},
					{selectValue("CASE WHEN FALSE THEN "+expr+" ELSE NULL::"+tc.typ+" END", " WHERE id=1", nil), false, true},
					// A surviving nonconstant call initializes even without rows.
					{fixture.Select("SELECT "+expr+" AS v FROM og_soundex_source WHERE id>99", []string{"v"}, []string{tc.typ}, nil), true, true},
					{fixture.Select("SELECT "+expr+" AS v FROM og_soundex_source WHERE FALSE", []string{"v"}, []string{tc.typ}, nil), true, true},
				}
				if tc.name == "difference" {
					cases = append(cases,
						objectGrantProviderCase{selectValue(expr, " WHERE id=4", nil), true, true},
						objectGrantProviderCase{selectValue(call("source", "$1::TEXT"), " WHERE id=1", tc.value, "Andrew"), true, true},
						objectGrantProviderCase{selectValue(call("source", "NULL::TEXT"), " WHERE id=1", nil), false, true},
						objectGrantProviderCase{selectValue(call("source", "$1::TEXT"), " WHERE id=1", nil, nil), false, false},
					)
				}
				// Revoke only the selected member, never a synthesized provider
				// family permission. The other genuine member remains usable.
				control := fixture.Select("SELECT public.difference(source,other) AS v FROM og_soundex_source WHERE id=1", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}})
				if tc.name == "difference" {
					control = fixture.Select("SELECT public.soundex(source) AS v FROM og_soundex_source WHERE id=1", []string{"v"}, []string{"TEXT"}, [][]any{{"A500"}})
				}
				objectGrantProviderLifecycle(t, p, b, c, run, tc.target, "function", tc.name, cases, []fixture.Step{control})
				// The lifecycle helper restores PUBLIC access on return.
				// Revoke again before proving fresh preparation is effect-free
				// admission, with EXECUTE checked only when the call is run.
				defer objectGrantRoutineCleanup(t, p, c, []string{tc.target})
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+tc.target+" FROM PUBLIC,og_object_reader", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					for _, query := range []string{cases[0].step.Query, cases[1].step.Query} {
						stmt, err := c.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						func() {
							defer stmt.Close()
							denied := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + tc.name})
							denied.SelectRows, denied.ErrorStage = true, "execute"
							fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}, b.Recorder, denied)
						}()
					}
				})
			})
		})
	}
}
