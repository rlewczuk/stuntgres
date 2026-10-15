package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Independent expectations: PG18 section 9.19, section 8.15.4, and REL_18_0
// array_userfuncs.c array_append/array_prepend/array_cat; pinned PG18.0 oracle.
func TestReferenceArrayTransformConcat(t *testing.T) {
	arrayTransformScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		arrayTransformValues(t, p, b, c, []arrayTransformValue{
			{"append_bounded", "array_append('[0:1]={2,3}'::int[],4)", "_INT4", "[0:2]={2,3,4}"},
			{"prepend_bounded", "array_prepend(1,'[0:1]={2,3}'::int[])", "_INT4", "[0:2]={1,2,3}"},
			{"append_nulls", "array_append(NULL::int[],NULL::int)", "_INT4", "{NULL}"},
			{"prepend_nulls", "array_prepend(NULL::int,NULL::int[])", "_INT4", "{NULL}"},
			{"append_empty", "array_append('{}'::int[],8)", "_INT4", "{8}"},
			{"prepend_empty", "array_prepend(8,'{}'::int[])", "_INT4", "{8}"},
			{"cat_nulls", "array_cat(NULL::int[],NULL::int[])", "_INT4", nil},
			{"cat_empty", "array_cat('{}'::int[],'{}'::int[])", "_INT4", "{}"},
			{"cat_null_empty", "array_cat(NULL::int[],'{}'::int[])", "_INT4", "{}"},
			{"cat_empty_null", "array_cat('{}'::int[],NULL::int[])", "_INT4", "{}"},
			{"cat_left_null", "array_cat(NULL::int[],'[0:1]={1,2}'::int[])", "_INT4", "[0:1]={1,2}"},
			{"cat_right_null", "array_cat('[0:1]={1,2}'::int[],NULL::int[])", "_INT4", "[0:1]={1,2}"},
			{"cat_left_empty", "array_cat('{}'::int[],'[0:1]={1,2}'::int[])", "_INT4", "[0:1]={1,2}"},
			{"cat_right_empty", "array_cat('[0:1]={1,2}'::int[],'{}'::int[])", "_INT4", "[0:1]={1,2}"},
			{"cat_distinct_bounds", "array_cat('[0:1]={1,2}'::int[],'[9:10]={3,4}'::int[])", "_INT4", "[0:3]={1,2,3,4}"},
			{"cat_rows", "array_cat('[0:0][5:6]={{1,2}}'::int[],'[9:9][5:6]={{3,4}}'::int[])", "_INT4", "[0:1][5:6]={{1,2},{3,4}}"},
			{"cat_rank_up", "array_cat('[5:6]={1,2}'::int[],'[-2:-1][5:6]={{3,4},{5,6}}'::int[])", "_INT4", "[-2:0][5:6]={{1,2},{3,4},{5,6}}"},
			{"cat_rank_down", "array_cat('[-2:-1][5:6]={{3,4},{5,6}}'::int[],'[5:6]={1,2}'::int[])", "_INT4", "[-2:0][5:6]={{3,4},{5,6},{1,2}}"},
			{"operator_cat", "'[0:1]={1,2}'::int[] || '[9:10]={3,4}'::int[]", "_INT4", "[0:3]={1,2,3,4}"},
			{"operator_append", "ARRAY[1,2] || 3::int", "_INT4", "{1,2,3}"},
			{"operator_prepend", "0::int || ARRAY[1,2]", "_INT4", "{0,1,2}"},
			{"operator_unknown_array", "ARRAY[1,2] || '{3,4}'", "_INT4", "{1,2,3,4}"},
			{"operator_unknown_left", "'{0}' || ARRAY[1,2]", "_INT4", "{0,1,2}"},
			{"operator_unknown_null", "ARRAY[1,2] || NULL", "_INT4", "{1,2}"},
			{"operator_typed_null", "ARRAY[1,2] || NULL::int", "_INT4", "{1,2,NULL}"},
			{"operator_text_scalar", "'a'::text || ARRAY['b','c']::text[]", "_TEXT", "{a,b,c}"},
			{"operator_qualified", "ARRAY[1] OPERATOR(pg_catalog.||) ARRAY[2]", "_INT4", "{1,2}"},
			{"unknown_text_concat", "'a' || 'b'", "TEXT", "ab"},
			{"append_json_no_comparison", "array_append(ARRAY['1'::json],'2'::json)", "_JSON", "{1,2}"},
		})
		for _, tc := range []struct {
			name  string
			value any
			want  any
		}{
			{"unknown_parameter_array", "{3,4}", "{1,2,3,4}"},
			{"unknown_parameter_null", nil, "{1,2}"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				arrayTransformBoth(t, p, b, c, arrayTransformSelect(b, "SELECT ARRAY[1,2] || $1 AS a FROM at_one", []string{"a"}, []string{"_INT4"}, [][]any{{tc.want}}, tc.value))
			})
		}
	})
}

func TestReferenceArrayTransformTyping(t *testing.T) {
	arrayTransformScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		arrayTransformValues(t, p, b, c, []arrayTransformValue{
			{"append_widen", "array_append(ARRAY[1::int2],2::int8)", "_INT8", "{1,2}"},
			{"prepend_widen", "array_prepend(1::int8,ARRAY[2::int2])", "_INT8", "{1,2}"},
			{"cat_numeric", "array_cat(ARRAY[1],ARRAY[2.5::numeric])", "_NUMERIC", "{1,2.5}"},
			{"operator_widen", "ARRAY[1::int2] || 2::int8", "_INT8", "{1,2}"},
			{"unknown_append", "array_append(NULL,NULL)", "_TEXT", "{NULL}"},
			{"unknown_prepend", "array_prepend(NULL,NULL)", "_TEXT", "{NULL}"},
			{"unknown_cat", "array_cat(NULL,NULL)", "_TEXT", nil},
			{"unknown_strings", "array_append('{a}','b')", "_TEXT", "{a,b}"},
			{"simple_typmod", "array_reverse(ARRAY['ab'::varchar(2)])", "_VARCHAR", "{ab}"},
		})
		for _, tc := range []arrayTransformValue{
			{"singleton_append", "array_append('{1}'::int[],2)", "_INT4", "{1,2}"},
			{"singleton_prepend", "array_prepend(0,'{1}'::int[])", "_INT4", "{0,1}"},
			{"singleton_cat", "array_cat('{1}'::int[],'{2}'::int[])", "_INT4", "{1,2}"},
			{"singleton_fill", "array_fill(7,'{2}'::int[])", "_INT4", "{7,7}"},
			{"singleton_reverse", "array_reverse('{1,2}'::int[])", "_INT4", "{2,1}"},
			{"singleton_sort", "array_sort('{2,1}'::int[])", "_INT4", "{1,2}"},
			{"singleton_trim", "trim_array('{1,2}'::int[],1)", "_INT4", "{1}"},
			{"singleton_string", "array_to_string('{1,2}'::int[],',')", "TEXT", "1,2"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				arrayTransformBoth(t, p, b, c, arrayTransformSelect(b, "SELECT "+tc.expr+" AS a", []string{"a"}, []string{tc.typ}, [][]any{{tc.value}}))
			})
		}
		for _, q := range []string{
			"CREATE DOMAIN at_leaf AS INT CHECK (VALUE > 0)",
			"CREATE DOMAIN at_outer AS INT[]",
			"CREATE DOMAIN at_vector AS INT[]",
			"CREATE TYPE at_mood AS ENUM ('low','high')",
			"CREATE TYPE at_pair AS (n INT,s TEXT)",
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		for _, tc := range []struct{ name, expr string }{
			{"agree_domain", "array_append(ARRAY[1::at_leaf],2::at_leaf)"},
			{"domain_unknown", "array_append(ARRAY[1::at_leaf],NULL)"},
			{"mixed_domain", "array_append(ARRAY[1::at_leaf],2::int)"},
			{"agree_domain_cat", "array_cat(ARRAY[1::at_leaf],ARRAY[2::at_leaf])"},
			{"agree_domain_prepend", "array_prepend(1::at_leaf,ARRAY[2::at_leaf])"},
			{"agree_domain_operator", "ARRAY[1::at_leaf] || 2::at_leaf"},
			{"mixed_domain_cat", "array_cat(ARRAY[1::at_leaf],ARRAY[2::int])"},
			{"domain_trim", "trim_array(ARRAY[1::at_leaf,2::at_leaf],1)"},
			{"outer_sort", "array_sort('{2,1}'::at_outer)"},
			{"domain_null", "array_reverse(NULL::at_leaf[])"},
			{"outer_domain", "array_reverse('{1,2}'::at_outer)"},
			{"outer_compatible", "array_cat('{1}'::at_outer,ARRAY[2])"},
			{"domain_fill", "array_fill(1::at_leaf,ARRAY[2])"},
			{"array_domain_fill", "array_fill('{1,2}'::at_vector,ARRAY[2])"},
			{"array_domain_append", "array_append(ARRAY['{1}'::at_vector],'{2}'::at_vector)"},
			{"enum_fill", "array_fill('low'::at_mood,ARRAY[2])"},
			{"composite_reverse", "array_reverse(ARRAY[ROW(1,'a')::at_pair,ROW(2,'b')::at_pair])"},
			{"domain_zero_rows", "array_sort(ARRAY[1::at_leaf])"},
			{"typmod_cat", "array_cat(ARRAY['ab'::varchar(2)],ARRAY['cde'::varchar(3)])"},
		} {
			arrayTransformTypingContract(t, p, b, c, tc.name, tc.expr)
			if tc.name == "domain_zero_rows" {
				arrayTransformAssertContract(t, p, b, c, tc.name+"_empty", "SELECT "+tc.expr+" AS a FROM at_one WHERE FALSE")
			}
		}
		for _, expr := range []string{
			"array_to_string(attmissingval,',')", "array_reverse(attmissingval)",
			"array_sort(attmissingval)", "trim_array(attmissingval,0)",
			"array_append(attmissingval,NULL)", "array_cat(attmissingval,attmissingval)",
			"array_prepend(NULL,attmissingval)", "array_fill(attmissingval,ARRAY[1])",
		} {
			arrayTransformAssertContract(t, p, b, c, expr, "SELECT "+expr+" AS a FROM pg_catalog.pg_attribute WHERE attrelid='at_one'::regclass::oid AND attname='id'")
		}
		for _, expr := range []string{"'a'::text || 'b'", "'\\x01'::bytea || '\\x02'", "B'10' || '01'"} {
			arrayTransformAssertContract(t, p, b, c, expr, "SELECT "+expr+" AS a FROM at_one")
		}
	})
}

// REL_18_0 arrayfuncs.c array_fill_internal validates vectors before creating
// the result. Length-vector storage bounds do not become result bounds.
func TestReferenceArrayTransformFill(t *testing.T) {
	arrayTransformScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		arrayTransformValues(t, p, b, c, []arrayTransformValue{
			{"bounded", "array_fill(7,ARRAY[2,3],ARRAY[-1,5])", "_INT4", "[-1:0][5:7]={{7,7,7},{7,7,7}}"},
			{"null_element", "array_fill(NULL::int,ARRAY[2])", "_INT4", "{NULL,NULL}"},
			{"vector_bounds_ignored", "array_fill(7,'[5:6]={2,3}'::int[],'[-2:-1]={-1,5}'::int[])", "_INT4", "[-1:0][5:7]={{7,7,7},{7,7,7}}"},
			{"empty_dimensions", "array_fill(7,'{}'::int[])", "_INT4", "{}"},
			{"zero", "array_fill(7,ARRAY[2,0,3])", "_INT4", "{}"},
			{"six", "array_fill(7,ARRAY[1,1,1,1,1,1])", "_INT4", "{{{{{{7}}}}}}"},
			{"json_no_order", "array_fill('1'::json,ARRAY[2])", "_JSON", "{1,1}"},
			{"text_value", "array_fill('x'::text,ARRAY[2])", "_TEXT", "{x,x}"},
		})
		for _, expr := range []string{
			"array_fill(1,ARRAY[-1])", "array_fill(1,ARRAY[0,-1])", "array_fill(1,ARRAY[-1,0])",
			"array_fill(1,ARRAY[2147483647,2])", "array_fill(1,ARRAY[2],ARRAY[2147483647])",
			"array_fill(1,ARRAY[0],ARRAY[-2147483648])", "array_fill(1,ARRAY[0],ARRAY[2147483647])",
			"array_fill(1,ARRAY[0,NULL])", "array_fill(1,ARRAY[0],ARRAY[NULL]::int[])",
			"array_fill(1,NULL::int[],NULL::int[])", "array_fill(1,ARRAY[1,NULL],ARRAY[1])",
			"array_fill(1,ARRAY[1,1,1,1,1,1,1],NULL::int[])",
		} {
			arrayTransformAssertContract(t, p, b, c, expr, "SELECT "+expr+" AS a FROM at_one")
		}
	})
}
