package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceArrayTransformFeatureSmoke(t *testing.T) {
	arrayTransformScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// One cohesive projection, independently specified rather than a call to
		// another top-level test. All eight functions and three || overloads.
		cases := []arrayTransformValue{
			{"appended", "array_append('[0:1]={2,3}'::int[],4)", "_INT4", "[0:2]={2,3,4}"},
			{"prepended", "array_prepend(1,'[0:1]={2,3}'::int[])", "_INT4", "[0:2]={1,2,3}"},
			{"concatenated", "array_cat('[0:1]={1,2}'::int[],'[9:10]={3,4}'::int[])", "_INT4", "[0:3]={1,2,3,4}"},
			{"element_right", "ARRAY[1,2] || 3::int", "_INT4", "{1,2,3}"},
			{"element_left", "0::int || ARRAY[1,2]", "_INT4", "{0,1,2}"},
			{"arrays", "ARRAY[1,2] || ARRAY[3,4]", "_INT4", "{1,2,3,4}"},
			{"filled", "array_fill(7,ARRAY[2,3],ARRAY[-1,5])", "_INT4", "[-1:0][5:7]={{7,7,7},{7,7,7}}"},
			{"null_filled", "array_fill(NULL::int,ARRAY[2])", "_INT4", "{NULL,NULL}"},
			{"reversed", "array_reverse(" + arrayTransformMatrix + ")", "_INT4", "[-1:1][5:6]={{5,6},{3,NULL},{1,2}}"},
			{"trimmed", "trim_array(" + arrayTransformMatrix + ",1)", "_INT4", "{{1,2},{3,NULL}}"},
			{"sorted_rows", "array_sort('[10:12][20:21]={{1,2},{10,20},{3,4}}'::int[])", "_INT4", "[10:12][20:21]={{1,2},{3,4},{10,20}}"},
			{"ascending", "array_sort('{3,NULL,1,2,NULL}'::int[])", "_INT4", "{1,2,3,NULL,NULL}"},
			{"descending", "array_sort('{3,NULL,1,2,NULL}'::int[],true)", "_INT4", "{NULL,NULL,3,2,1}"},
			{"descending_last", "array_sort('{3,NULL,1,2,NULL}'::int[],true,false)", "_INT4", "{3,2,1,NULL,NULL}"},
			{"ascending_first", "array_sort('{3,NULL,1,2,NULL}'::int[],false,true)", "_INT4", "{NULL,NULL,1,2,3}"},
			{"omitted", "array_to_string('{{1,NULL},{2,3}}'::int[],',')", "TEXT", "1,2,3"},
			{"marked", "array_to_string('{{1,NULL},{2,3}}'::int[],',','*')", "TEXT", "1,*,2,3"},
			{"null_concat", "array_cat(NULL::int[],NULL::int[])", "_INT4", nil},
			{"empty_concat", "array_cat('{}'::int[],'{}'::int[])", "_INT4", "{}"},
		}
		exprs, names, types := make([]string, len(cases)), make([]string, len(cases)), make([]string, len(cases))
		values := make([]any, len(cases))
		for i, tc := range cases {
			exprs[i], names[i], types[i], values[i] = tc.expr+" AS "+tc.name, tc.name, tc.typ, tc.value
		}
		arrayTransformBoth(t, p, b, c, arrayTransformSelect(b, "SELECT "+strings.Join(exprs, ",")+" FROM at_one", names, types, [][]any{values}))
		arrayTransformBoth(t, p, b, c, fixture.Select("SELECT array_dims(array_fill(7,ARRAY[2,3],ARRAY[-1,5])) AS dimensions,array_ndims(array_reverse("+arrayTransformMatrix+")) AS rank,cardinality(trim_array("+arrayTransformMatrix+",1)) AS leaves FROM at_one", []string{"dimensions", "rank", "leaves"}, []string{"TEXT", "INT4", "INT4"}, [][]any{{"[-1:0][5:7]", int64(2), int64(4)}}))
		arrayTransformDiagnostic(t, p, b, c, "SELECT array_cat('{1}'::int[],'{{{2}}}'::int[]) AS a FROM at_one", fixture.Diagnostic{Code: "2202E", Message: "cannot concatenate incompatible arrays", Detail: "Arrays of 1 and 3 dimensions are not compatible for concatenation."}, false)
		arrayTransformHeldTrim(t, p, b, c)
		check := arrayTransformStoredSetup(t, p, b, c)
		arrayTransformBoth(t, p, b, c, check)
		arrayTransformNativeReplay(t, p, b, c, check)
	})
}
