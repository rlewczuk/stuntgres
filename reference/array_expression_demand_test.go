package reference_test

import (
	"database/sql"
	"testing"
)

// Source-derived expectations from REL_18_0 ExecInitSubscriptingRef: base,
// all upper bounds, all lower bounds, NULL-index check, replacement RHS.
func TestReferenceArrayExpressionDemandPrecedence(t *testing.T) {
	cases := []struct{ name, q, code, message string }{
		{"uppers_before_lowers", "UPDATE public.ax SET m[(2147483647+id):1][1:(1/(id-id))]=ARRAY[7] WHERE id=1", "22012", "division by zero"},
		{"upper_first_error", "UPDATE public.ax SET m[1:(2147483647+id)][(1/(id-id)):1]=ARRAY[7] WHERE id=1", "22003", "integer out of range"},
		{"null_check_before_rhs", "UPDATE public.ax SET a[NULL]=1/(id-id) WHERE id=1", "22004", "array subscript in assignment must not be null"},
		{"lower_demand_before_null_check", "UPDATE public.ax SET a[(1/(id-id)):NULL]=ARRAY[7] WHERE id=1", "22012", "division by zero"},
		{"upper_demand_before_null_lower", "UPDATE public.ax SET a[NULL:(1/(id-id))]=ARRAY[7] WHERE id=1", "22012", "division by zero"},
		{"null_base_assignment_demands_bounds", "UPDATE public.ax SET a[1/(id-id)]=7 WHERE id=4", "22012", "division by zero"},
		{"read_bounds_before_null_check", "SELECT a[NULL][1/(id-id)] FROM public.ax WHERE id=1", "22012", "division by zero"},
		{"rhs_before_wrong_rank", "UPDATE public.ax SET m[1]=1/(id-id) WHERE id=1", "22012", "division by zero"},
		{"rhs_before_inverted_slice", "UPDATE public.ax SET a[2:1]=ARRAY[1/(id-id)] WHERE id=1", "22012", "division by zero"},
		{"earlier_mutation_before_later_rhs", "UPDATE public.ax SET a[1:2]=ARRAY[7],a[1]=1/(id-id) WHERE id=1", "2202E", "source array too small"},
	}
	for _, tc := range cases {
		for _, prepared := range []bool{false, true} {
			name := tc.name
			if prepared {
				name += "_prepared"
			}
			t.Run(name, func(t *testing.T) {
				arrayExpressionScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
					arrayExpressionFixture(t, p, b, c)
					s := referenceErrorDiagnostic(tc.q, controlsReferenceDiagnostic(tc.code, tc.message))
					s.selectRows = tc.q[0] == 'S'
					s.prepared = prepared
					s.errorStage = "execute"
					referenceRunStep(t, p.ctx, c, b.recorder, s)
					referenceRunStep(t, p.ctx, c, b.recorder, arrayReferenceSelect("SELECT id,a,m FROM public.ax ORDER BY id", []string{"id", "a", "m"}, []string{"INT4", "_INT4", "_INT4"}, []string{"INT4", "_INT4", "_INT4"}, [][]any{{int64(1), "{10,NULL,30}", "{{1,2},{3,4}}"}, {int64(2), "[0:2]={40,50,60}", "[-1:0][3:4]={{5,6},{7,8}}"}, {int64(3), "{}", "{}"}, {int64(4), nil, nil}}, b.pg))
				})
			})
		}
	}
	t.Run("null_read_skips_bounds", func(t *testing.T) {
		arrayExpressionScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
			arrayExpressionFixture(t, p, b, c)
			arrayExpressionBoth(t, p, b, c, arrayReferenceSelect("SELECT a[1/(id-id)] AS e,a[(2147483647+id):(1/(id-id))] AS s FROM public.ax WHERE id=4", []string{"e", "s"}, []string{"INT4", "_INT4"}, []string{"INT4", "_INT4"}, [][]any{{nil, nil}}, b.pg))
		})
	})
	for _, prepared := range []bool{false, true} {
		name := "null_check_skips_volatile_rhs"
		if prepared {
			name += "_prepared"
		}
		t.Run(name, func(t *testing.T) {
			arrayExpressionScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				arrayExpressionFixture(t, p, b, c)
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE SEQUENCE public.ax_demand_seq START WITH 40", 0))
				referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT nextval('public.ax_demand_seq') AS v FROM public.one", []string{"v"}, []string{"INT8"}, [][]any{{int64(40)}}))
				s := referenceErrorDiagnostic("UPDATE public.ax SET a[NULL]=nextval('public.ax_demand_seq') WHERE id=1", controlsReferenceDiagnostic("22004", "array subscript in assignment must not be null"))
				s.prepared = prepared
				s.errorStage = "execute"
				referenceRunStep(t, p.ctx, c, b.recorder, s)
				referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT currval('public.ax_demand_seq') AS v FROM public.one", []string{"v"}, []string{"INT8"}, [][]any{{int64(40)}}))
			})
		})
	}
	for _, prepared := range []bool{false, true} {
		name := "losing_join_pair"
		if prepared {
			name += "_prepared"
		}
		t.Run(name, func(t *testing.T) {
			arrayExpressionScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				arrayExpressionFixture(t, p, b, c)
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE public.ax_demand(v INT)", 0))
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO public.ax_demand VALUES(1),(3)", 2))
				s := referenceErrorDiagnostic("UPDATE public.ax AS x SET m[q.v][1]=7 FROM public.ax_demand AS q WHERE x.id=1", controlsReferenceDiagnostic("2202E", "array subscript out of range"))
				s.prepared = prepared
				s.errorStage = "execute"
				referenceRunStep(t, p.ctx, c, b.recorder, s)
				referenceRunStep(t, p.ctx, c, b.recorder, arrayReferenceSelect("SELECT m FROM public.ax WHERE id=1", []string{"m"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"{{1,2},{3,4}}"}}, b.pg))
			})
		})
	}
}
