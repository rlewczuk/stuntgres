package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceArrayExpressionQueryConsumers(t *testing.T) {
	arrayExpressionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		arrayExpressionFixture(t, p, b, c)
		for _, s := range []fixture.Step{
			fixture.Select("SELECT a[1] AS v,count(*) AS n FROM public.ax WHERE id<3 GROUP BY a[1] ORDER BY a[1]", []string{"v", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(10), int64(1)}, {int64(50), int64(1)}}),
			fixture.Select("SELECT x.a[y.v] AS e FROM public.ax AS x JOIN public.one AS y ON x.a[y.v]>0 WHERE x.id=1", []string{"e"}, []string{"INT4"}, [][]any{{int64(10)}}),
			fixture.Select("SELECT sum(a[id]) AS s FROM public.ax WHERE id<3", []string{"s"}, []string{"INT8"}, [][]any{{int64(70)}}),
		} {
			arrayExpressionBoth(t, p, b, c, s)
		}
		s := fixture.Select("UPDATE public.ax SET a[1]=7 WHERE id=1 RETURNING a[1] AS e,a[1:2] AS s", []string{"e", "s"}, []string{"INT4", "_INT4"}, [][]any{{int64(7), "{7,NULL}"}})
		s.RawTypes = []string{"INT4", "_INT4"}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	})
}

func TestReferenceArrayExpressionPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ax_expression_reader"})
	arrayExpressionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE public.ax_acl (a INT[],hidden INT)", 0))
		run(fixture.Exec("INSERT INTO public.ax_acl VALUES ('{7}',1)", 1))
		q := "SELECT a[hidden] AS e FROM public.ax_acl"
		check := fixture.Select(q, []string{"e"}, []string{"INT4"}, [][]any{{int64(7)}})
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
		}
		for _, q := range []string{"CREATE ROLE ax_expression_reader", "GRANT SELECT(a) ON public.ax_acl TO ax_expression_reader", "SET SESSION AUTHORIZATION ax_expression_reader"} {
			run(fixture.Exec(q, 0))
		}
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
			}
		}()
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table ax_acl"))
			s.SelectRows = true
			s.Prepared = prepared
			run(s)
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(hidden) ON public.ax_acl TO ax_expression_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION ax_expression_reader", 0))
		arrayExpressionBoth(t, p, b, c, check)
	})
}
