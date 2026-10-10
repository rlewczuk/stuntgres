package reference_test

import (
	"database/sql"
	"testing"
)

func arrayReviewFixtureRows() [][]any {
	return [][]any{{int64(1), "{10,NULL,30}", "{{1,2},{3,4}}"}, {int64(2), "[0:2]={40,50,60}", "[-1:0][3:4]={{5,6},{7,8}}"}, {int64(3), "{}", "{}"}, {int64(4), nil, nil}}
}

func arrayReviewCheck(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, rows [][]any) {
	t.Helper()
	referenceRunStep(t, p.ctx, c, b.recorder, arrayReferenceSelect("SELECT id,a,m FROM public.ax ORDER BY id", []string{"id", "a", "m"}, []string{"INT4", "_INT4", "_INT4"}, []string{"INT4", "_INT4", "_INT4"}, rows, b.pg))
}

// Independent source expectations: REL_18_0 array_set_slice validates each
// existing dimension's inversion/range, then final destination shape/bounds,
// before source sufficiency. Its ndim=0 path instead constructs supplied spans;
// ArrayGetNItems rejects negative spans and construct_md_array canonicalizes zero.
// MaxArraySize = 0x3fffffff / sizeof(Datum) = 134217727 in the pinned x64 oracle.
// https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/utils/adt/arrayfuncs.c
// https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/utils/adt/arrayutils.c
func TestReferenceArrayExpressionReviewRegression(t *testing.T) {
	failures := []struct{ name, q, code, message, detail string }{
		{"bounds_before_short_source", "UPDATE public.ax SET m[1:3][1:1]=ARRAY[7] WHERE id=1", "2202E", "array subscript out of range", ""},
		{"rank_before_inversion", "UPDATE public.ax SET a[2:1][1:1]=ARRAY[7] WHERE id=1", "2202E", "wrong number of array subscripts", ""},
		{"earlier_bounds_before_later_inversion", "UPDATE public.ax SET m[1:3][2:1]=ARRAY[7] WHERE id=1", "2202E", "array subscript out of range", ""},
		{"earlier_low_bounds_before_later_inversion", "UPDATE public.ax SET m[0:1][2:1]=ARRAY[7] WHERE id=1", "2202E", "array subscript out of range", ""},
		{"earlier_inversion_before_later_bounds", "UPDATE public.ax SET m[2:1][1:3]=ARRAY[7] WHERE id=1", "2202E", "upper bound cannot be less than lower bound", ""},
		{"final_shape_before_short_source", "UPDATE public.ax SET a[1:2147483647]=ARRAY[7] WHERE id=1", "54000", "array size exceeds the maximum allowed (134217727)", ""},
		{"element_enlargement_overflow", "UPDATE public.ax SET a[-2147483648]=7 WHERE id=1", "54000", "array size exceeds the maximum allowed (134217727)", ""},
		{"insert_negative_span", "INSERT INTO public.ax(id,a[3:1]) VALUES(5,ARRAY[7])", "54000", "array size exceeds the maximum allowed (134217727)", ""},
		{"empty_negative_span", "UPDATE public.ax SET a[3:1]=ARRAY[7] WHERE id=3", "54000", "array size exceeds the maximum allowed (134217727)", ""},
		{"null_negative_span", "UPDATE public.ax SET a[3:1]=ARRAY[7] WHERE id=4", "54000", "array size exceeds the maximum allowed (134217727)", ""},
		{"zero_then_negative_span", "INSERT INTO public.ax(id,m[2:1][3:1]) VALUES(5,ARRAY[7])", "54000", "array size exceeds the maximum allowed (134217727)", ""},
		{"overflow_span", "INSERT INTO public.ax(id,a[-2147483648:2147483647]) VALUES(5,ARRAY[7])", "54000", "array size exceeds the maximum allowed (134217727)", ""},
		{"singleton_bound_overflow", "INSERT INTO public.ax(id,a[2147483647:2147483647]) VALUES(5,ARRAY[7])", "54000", "array lower bound is too large: 2147483647", ""},
		{"zero_span_missing_boundary", "INSERT INTO public.ax(id,m[2:1][:1]) VALUES(5,ARRAY[7])", "2202E", "array slice subscript must provide both boundaries", "When assigning to a slice of an empty array value, slice boundaries must be fully specified."},
		// construct_md_array checks final bounds even when another span is zero.
		{"zero_span_with_earlier_bound_overflow", "INSERT INTO public.ax(id,m[2147483647:2147483647][2:1]) VALUES(5,ARRAY[7])", "54000", "array lower bound is too large: 2147483647", ""},
		{"zero_span_with_later_bound_overflow", "INSERT INTO public.ax(id,m[2:1][2147483647:2147483647]) VALUES(5,ARRAY[7])", "54000", "array lower bound is too large: 2147483647", ""},
		{"empty_source_before_final_bounds", "INSERT INTO public.ax(id,a[2147483647:2147483647]) VALUES(5,ARRAY[]::int[])", "2202E", "source array too small", ""},
	}
	for _, tc := range failures {
		for _, prepared := range []bool{false, true} {
			name := tc.name
			if prepared {
				name += "_prepared"
			}
			t.Run(name, func(t *testing.T) {
				arrayExpressionScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
					arrayExpressionFixture(t, p, b, c)
					diagnostic := controlsReferenceDiagnostic(tc.code, tc.message)
					diagnostic.Detail = tc.detail
					step := referenceErrorDiagnostic(tc.q, diagnostic)
					step.prepared = prepared
					step.errorStage = "execute"
					referenceRunStep(t, p.ctx, c, b.recorder, step)
					arrayReviewCheck(t, p, b, c, arrayReviewFixtureRows())
				})
			})
		}
	}
	successes := []struct {
		name, q  string
		updateID int
		insert   bool
	}{
		{"insert_zero_span", "INSERT INTO public.ax(id,a[2:1]) VALUES(5,ARRAY[7])", 0, true},
		{"empty_zero_span", "UPDATE public.ax SET a[2:1]=ARRAY[7] WHERE id=3", 3, false},
		{"null_zero_span", "UPDATE public.ax SET a[2:1]=ARRAY[7] WHERE id=4", 4, false},
		{"insert_multidimensional_zero_span", "INSERT INTO public.ax(id,a[1:1][2:1]) VALUES(5,ARRAY[7])", 0, true},
		{"empty_multidimensional_zero_span", "UPDATE public.ax SET a[1:1][2:1]=ARRAY[7] WHERE id=3", 3, false},
		{"null_multidimensional_zero_span", "UPDATE public.ax SET a[1:1][2:1]=ARRAY[7] WHERE id=4", 4, false},
		{"insert_zero_span_empty_source", "INSERT INTO public.ax(id,a[2:1]) VALUES(5,ARRAY[]::int[])", 0, true},
		{"insert_negative_span_null_rhs", "INSERT INTO public.ax(id,a[3:1]) VALUES(5,NULL)", 0, true},
		{"empty_negative_span_null_rhs", "UPDATE public.ax SET a[3:1]=NULL WHERE id=3", 3, false},
		{"null_negative_span_null_rhs", "UPDATE public.ax SET a[3:1]=NULL WHERE id=4", 4, false},
		{"zero_span_maximum_valid_lower_bound", "INSERT INTO public.ax(id,a[2147483647:2147483646][1:1]) VALUES(5,ARRAY[7])", 0, true},
	}
	for _, tc := range successes {
		for _, prepared := range []bool{false, true} {
			name := tc.name
			if prepared {
				name += "_prepared"
			}
			t.Run(name, func(t *testing.T) {
				arrayExpressionScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
					arrayExpressionFixture(t, p, b, c)
					step := referenceExec(tc.q, 1)
					step.prepared = prepared
					referenceRunStep(t, p.ctx, c, b.recorder, step)
					rows := arrayReviewFixtureRows()
					if tc.insert {
						rows = append(rows, []any{int64(5), "{}", nil})
					} else {
						rows[tc.updateID-1][1] = "{}"
					}
					arrayReviewCheck(t, p, b, c, rows)
				})
			})
		}
	}
}
