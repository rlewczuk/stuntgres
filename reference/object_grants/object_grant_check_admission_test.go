package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func objectGrantCheckExpression(folded bool) string {
	if folded {
		return "pg_catalog.json_array_length('[1,2]'::json)>0"
	}
	return "pg_catalog.json_array_length(j)>0"
}

func objectGrantCheckDenied(query string, args ...any) fixture.Step {
	s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function json_array_length"}, args...)
	s.SelectRows, s.ErrorStage = true, "execute"
	return s
}

func TestReferenceObjectGrantCheckAdmissionLifecycle(t *testing.T) {
	for _, folded := range []bool{false, true} {
		t.Run(map[bool]string{false: "row", true: "folded"}[folded], func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				defer objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog.json_array_length(json)"})
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE public.check_lifecycle(k INT,j JSON,CHECK("+objectGrantCheckExpression(folded)+"))", 0))
				run(fixture.Exec("INSERT INTO public.check_lifecycle VALUES(99,'[9]')", 1))
				run(fixture.Exec("GRANT INSERT,SELECT ON public.check_lifecycle TO og_object_reader", 0))
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM PUBLIC", 0))
				query := "INSERT INTO public.check_lifecycle VALUES($1,'[1,2]') RETURNING k"
				stmt, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
				deny := func() {
					fixture.RoleGrantActor(run, "og_object_reader", func() {
						run(objectGrantCheckDenied(query, int64(7)))
						fixture.RunStep(t, p.Ctx, held, b.Recorder, objectGrantCheckDenied(query, int64(7)))
						run(fixture.Exec("INSERT INTO public.check_lifecycle SELECT 7,'[1]'::json FROM public.check_lifecycle WHERE FALSE", 0))
						run(fixture.Select("SELECT k FROM public.check_lifecycle ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(99)}}))
					})
				}
				insert := func() {
					fixture.RoleGrantActor(run, "og_object_reader", func() {
						fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}}, int64(7)))
						run(fixture.Select(query, []string{"k"}, []string{"INT4"}, [][]any{{int64(8)}}, int64(8)))
						run(fixture.Exec("INSERT INTO public.check_lifecycle VALUES(9,'[1]')", 1))
						run(fixture.Select("SELECT k FROM public.check_lifecycle ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}, {int64(9)}, {int64(99)}}))
					})
					// Remove only new rows so the same held handle is reused after revoke.
					run(fixture.Exec("DELETE FROM public.check_lifecycle WHERE k=7", 1))
					run(fixture.Exec("DELETE FROM public.check_lifecycle WHERE k=8", 1))
					run(fixture.Exec("DELETE FROM public.check_lifecycle WHERE k=9", 1))
				}
				deny()
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.json_array_length(json) TO og_object_reader", 0))
				insert()
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM og_object_reader", 0))
				deny()
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.json_array_length(json) TO og_object_reader", 0))
				insert()
			})
		})
	}
}

func TestReferenceObjectGrantCheckReadmissionFeatureSmoke(t *testing.T) {
	objectGrantCheckWaitScenario(t, true, "direct", false, true)
}
