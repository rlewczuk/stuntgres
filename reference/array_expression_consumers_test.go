package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceArrayExpressionQueryConsumers(t *testing.T) {
	arrayExpressionScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		arrayExpressionFixture(t, p, b, c)
		for _, s := range []referenceStep{
			referenceSelect("SELECT a[1] AS v,count(*) AS n FROM public.ax WHERE id<3 GROUP BY a[1] ORDER BY a[1]", []string{"v", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(10), int64(1)}, {int64(50), int64(1)}}),
			referenceSelect("SELECT x.a[y.v] AS e FROM public.ax AS x JOIN public.one AS y ON x.a[y.v]>0 WHERE x.id=1", []string{"e"}, []string{"INT4"}, [][]any{{int64(10)}}),
			referenceSelect("SELECT sum(a[id]) AS s FROM public.ax WHERE id<3", []string{"s"}, []string{"INT8"}, [][]any{{int64(70)}}),
		} {
			arrayExpressionBoth(t, p, b, c, s)
		}
		s := referenceSelect("UPDATE public.ax SET a[1]=7 WHERE id=1 RETURNING a[1] AS e,a[1:2] AS s", []string{"e", "s"}, []string{"INT4", "_INT4"}, [][]any{{int64(7), "{7,NULL}"}})
		s.rawTypes = []string{"INT4", "_INT4"}
		referenceRunStep(t, p.ctx, c, b.recorder, s)
	})
}

func TestReferenceArrayExpressionPrivileges(t *testing.T) {
	roleReferenceCleanup(t, []string{"ax_expression_reader"})
	arrayExpressionScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE public.ax_acl (a INT[],hidden INT)", 0))
		run(referenceExec("INSERT INTO public.ax_acl VALUES ('{7}',1)", 1))
		q := "SELECT a[hidden] AS e FROM public.ax_acl"
		check := referenceSelect(q, []string{"e"}, []string{"INT4"}, [][]any{{int64(7)}})
		if !b.pg {
			arrayExpressionBoth(t, p, b, c, check)
			return
		}
		// Public native ACL mutation is unavailable; native column manifest units
		// supplement this PostgreSQL column-grant phase during Stage C.
		for _, q := range []string{"CREATE ROLE ax_expression_reader", "GRANT USAGE ON SCHEMA public TO ax_expression_reader", "GRANT SELECT(a) ON public.ax_acl TO ax_expression_reader", "SET SESSION AUTHORIZATION ax_expression_reader"} {
			run(referenceExec(q, 0))
		}
		defer func() {
			if _, err := c.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
			}
		}()
		for _, prepared := range []bool{false, true} {
			s := referenceErrorDiagnostic(q, controlsReferenceDiagnostic("42501", "permission denied for table ax_acl"))
			s.selectRows = true
			s.prepared = prepared
			run(s)
		}
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("GRANT SELECT(hidden) ON public.ax_acl TO ax_expression_reader", 0))
		run(referenceExec("SET SESSION AUTHORIZATION ax_expression_reader", 0))
		arrayExpressionBoth(t, p, b, c, check)
	})
}
