package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Independently derived from REL_18_6 tsquery_rewrite_query and confirmed by the
// pinned PG18.6 oracle: fetch 100 before shape validation; process a fetched
// batch only while the tree is nonempty; fetch another 100 at the loop bottom.
// Volatile final-projection expressions distinguish actual fetch demand from
// sort materialization, rule application and final query equality.
func TestReferenceFTSQueryFunctionsSQLDemand(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		rows, deleteAt, failAt   int
		wrongShape, emptyInitial bool
		state, want              string
		calls                    int64
	}{
		{"shape_zero", 0, 0, 0, true, false, "22023", "", 0},
		{"shape_one", 1, 0, 0, true, false, "22023", "", 1},
		{"shape_99", 99, 0, 0, true, false, "22023", "", 99},
		{"shape_100", 100, 0, 0, true, false, "22023", "", 100},
		{"shape_101", 101, 0, 0, true, false, "22023", "", 100},
		{"shape_fail_1", 201, 0, 1, true, false, "22012", "", 1},
		{"shape_fail_100", 201, 0, 100, true, false, "22012", "", 100},
		{"shape_fail_101", 201, 0, 101, true, false, "22023", "", 100},
		{"shape_fail_200", 201, 0, 200, true, false, "22023", "", 100},
		{"shape_fail_201", 201, 0, 201, true, false, "22023", "", 100},
		{"delete_zero", 0, 1, 0, false, false, "", "'a'", 0},
		{"delete_one", 1, 1, 0, false, false, "", "", 1},
		{"delete_99", 99, 1, 0, false, false, "", "", 99},
		{"delete_100", 100, 1, 0, false, false, "", "", 100},
		{"delete_101", 101, 1, 0, false, false, "", "", 101},
		{"delete_200", 200, 1, 0, false, false, "", "", 200},
		{"delete_201", 201, 1, 0, false, false, "", "", 200},
		{"delete_at_100", 401, 100, 0, false, false, "", "", 200},
		{"delete_at_101", 401, 101, 0, false, false, "", "", 300},
		{"delete_at_200", 401, 200, 0, false, false, "", "", 300},
		{"delete_at_201", 401, 201, 0, false, false, "", "", 400},
		{"delete_fail_100", 201, 1, 100, false, false, "22012", "", 100},
		{"delete_fail_101", 201, 1, 101, false, false, "22012", "", 101},
		{"delete_fail_200", 201, 1, 200, false, false, "22012", "", 200},
		{"delete_fail_201", 201, 1, 201, false, false, "", "", 200},
		{"empty_initial", 201, 1, 1, false, true, "", "", 0},
		{"empty_initial_wrong_shape", 201, 0, 1, true, true, "", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					conn, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
					run(fixture.Exec("CREATE SEQUENCE fts_qf_demand_seq", 0))
					run(fixture.Exec("CREATE TABLE fts_qf_demand_rows(ord integer)", 0))
					if tc.rows != 0 {
						values := make([]string, tc.rows)
						for i := range values {
							values[i] = fmt.Sprintf("(%d)", i+1)
						}
						run(fixture.Exec("INSERT INTO fts_qf_demand_rows VALUES"+strings.Join(values, ","), int64(tc.rows)))
					}
					// ORDER BY has a nonvolatile key; nextval belongs to the final
					// projection, not an eager sort key or filtering side effect.
					var inner string
					if tc.wrongShape {
						inner = fmt.Sprintf("SELECT CASE WHEN nextval('fts_qf_demand_seq')>0 THEN 1/(ord-%d) ELSE 1 END,''::tsquery FROM fts_qf_demand_rows ORDER BY ord", tc.failAt)
					} else {
						inner = fmt.Sprintf("SELECT CASE WHEN nextval('fts_qf_demand_seq')=%d THEN 'a'::tsquery WHEN ord=%d THEN (1/(ord-%d))::text::tsquery ELSE 'inert'::tsquery END,''::tsquery FROM fts_qf_demand_rows ORDER BY ord", tc.deleteAt, tc.failAt, tc.failAt)
					}
					input := "a"
					if tc.emptyInitial {
						input = ""
					}
					query := "SELECT ts_rewrite(" + ftsTypesQuote(input) + "::tsquery," + ftsTypesQuote(inner) + ") AS rewritten"
					held, err := conn.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					for _, route := range []string{"direct", "prepared", "held_first", "held_reused"} {
						for _, queryMode := range []bool{false, true} {
							t.Run(fmt.Sprintf("%s_query_%t", route, queryMode), func(t *testing.T) {
								fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("ALTER SEQUENCE fts_qf_demand_seq RESTART WITH 1", 0))
								statement := held
								if route == "prepared" {
									var err error
									statement, err = conn.PrepareContext(p.Ctx, query)
									if err != nil {
										t.Fatal(err)
									}
									defer statement.Close()
								}
								t.Run("outcome", func(t *testing.T) {
									var s fixture.Step
									if tc.state != "" {
										d := ftsQueryFunctionsShapeError()
										if tc.state == "22012" {
											d = fixture.ControlsReferenceDiagnostic("22012", "division by zero")
										}
										s = fixture.ErrorDiagnostic(query, d)
										s.SelectRows = queryMode
										s.ErrorStage = "execute"
									} else if queryMode {
										s = fixture.Select(query, []string{"rewritten"}, []string{"TSQUERY"}, [][]any{{tc.want}})
									} else {
										s = fixture.Exec(query, 1)
									}
									if route == "direct" {
										fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
									} else {
										executor := fixture.HeldStatement{Executor: conn, Statement: statement, SQL: query}
										fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
									}
								})
								// Keep side-effect evidence even if the separate outcome
								// assertion fails; sequence effects survive statement errors.
								last, called := tc.calls, true
								if tc.calls == 0 {
									last, called = 1, false
								}
								fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT last_value,is_called FROM fts_qf_demand_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{last, called}}))
							})
						}
					}
				})
			}
		})
	}
}
