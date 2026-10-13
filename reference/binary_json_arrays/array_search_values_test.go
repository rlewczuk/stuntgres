package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceArraySearchValues(t *testing.T) {
	arraySearchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			name, expression, typ string
			want                  any
		}{
			{"first", "array_position($1::int[],7)", "INT4", int64(-2)},
			{"start", "array_position($1::int[],7,-1)", "INT4", int64(0)},
			{"below", "array_position($1::int[],7,-99)", "INT4", int64(-2)},
			{"last", "array_position($1::int[],7,2)", "INT4", int64(2)},
			{"above", "array_position($1::int[],7,3)", "INT4", nil},
			{"null_match", "array_position($1::int[],NULL)", "INT4", int64(-1)},
			{"absent", "array_position($1::int[],99)", "INT4", nil},
			{"positions", "array_positions($1::int[],7)", "_INT4", "{-2,0,2}"},
			{"null_positions", "array_positions($1::int[],NULL)", "_INT4", "{-1}"},
			{"absent_positions", "array_positions($1::int[],99)", "_INT4", "{}"},
			{"remove", "array_remove($1::int[],7)", "_INT4", "[-2:-1]={NULL,8}"},
			{"remove_null", "array_remove($1::int[],NULL)", "_INT4", "[-2:1]={7,7,8,7}"},
			{"remove_absent", "array_remove($1::int[],99)", "_INT4", arraySearchInput},
			{"replace_null", "array_replace($1::int[],NULL,9)", "_INT4", "[-2:2]={7,9,7,8,7}"},
			{"replace_with_null", "array_replace($1::int[],7,NULL)", "_INT4", "[-2:2]={NULL,NULL,NULL,8,NULL}"},
			{"replace_absent", "array_replace($1::int[],99,9)", "_INT4", arraySearchInput},
		} {
			t.Run(tc.name, func(t *testing.T) {
				arraySearchBoth(t, p, b, c, arraySearchSelect(b, "SELECT "+tc.expression+" AS v", []string{"v"}, []string{tc.typ}, [][]any{{tc.want}}, arraySearchInput))
			})
		}
		for _, tc := range []struct {
			name, q string
			types   []string
			want    []any
		}{
			{"null_array", "SELECT array_position(NULL::int[],NULL,NULL) AS p,array_positions(NULL::int[],NULL) AS ps,array_remove(NULL::int[],NULL) AS rm,array_replace(NULL::int[],NULL,NULL) AS rp", []string{"INT4", "_INT4", "_INT4", "_INT4"}, []any{nil, nil, nil, nil}},
			{"empty", "SELECT array_position('{}'::int[],1,NULL) AS p,array_positions('{}'::int[],1) AS ps,array_remove('{}'::int[],NULL) AS rm,array_replace('{}'::int[],NULL,1) AS rp", []string{"INT4", "_INT4", "_INT4", "_INT4"}, []any{nil, "{}", "{}", "{}"}},
			{"remove_all", "SELECT array_remove(ARRAY[7,7],7) AS a,array_remove(ARRAY[NULL]::int[],NULL) AS b", []string{"_INT4", "_INT4"}, []any{"{}", "{}"}},
			{"multidimensional_replace", "SELECT array_replace('[0:1][-1:0]={{1,NULL},{1,2}}'::int[],1,NULL) AS a,array_replace(ARRAY[[1,NULL],[1,2]],1,NULL) AS b", []string{"_INT4", "_INT4"}, []any{"[0:1][-1:0]={{NULL,NULL},{NULL,2}}", "{{NULL,NULL},{NULL,2}}"}},
			{"null_search_fastpath", "SELECT array_position(ARRAY[1],NULL,NULL) AS v", []string{"INT4"}, []any{nil}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				names := []string{"v"}
				if len(tc.want) == 4 {
					names = []string{"p", "ps", "rm", "rp"}
				}
				if len(tc.want) == 2 {
					names = []string{"a", "b"}
				}
				arraySearchBoth(t, p, b, c, arraySearchSelect(b, tc.q, names, tc.types, [][]any{tc.want}))
			})
		}
	})
}

func TestReferenceArraySearchMembership(t *testing.T) {
	arraySearchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			name, expr string
			want       any
		}{
			{"duplicate_required", "ARRAY[1] @> ARRAY[1,1]", true},
			{"duplicate_container", "ARRAY[1,1] @> ARRAY[1]", true},
			{"contained_duplicates", "ARRAY[1,1] <@ ARRAY[1]", true},
			{"absent", "ARRAY[1] @> ARRAY[1,2]", false},
			{"contained_absent", "ARRAY[1,2] <@ ARRAY[1]", false},
			{"flat_overlap", "ARRAY[[1,2],[3,4]] && ARRAY[4]", true},
			{"shape_irrelevant", "'[0:1][-1:0]={{1,2},{3,4}}'::int[] @> ARRAY[4,1]", true},
			{"order_irrelevant", "ARRAY[2,1] <@ '[8:9]={1,2}'::int[]", true},
			{"null_contains", "ARRAY[NULL]::int[] @> ARRAY[NULL]::int[]", false},
			{"null_contained", "ARRAY[NULL]::int[] <@ ARRAY[NULL]::int[]", false},
			{"null_overlap", "ARRAY[NULL]::int[] && ARRAY[NULL]::int[]", false},
			{"extra_null", "ARRAY[1,NULL] @> ARRAY[1]", true},
			{"required_null", "ARRAY[1] @> ARRAY[1,NULL]", false},
			{"empty_required", "ARRAY[1,NULL] @> '{}'::int[]", true},
			{"empty_contained", "'{}'::int[] <@ ARRAY[1]", true},
			{"empty_container", "'{}'::int[] @> ARRAY[1]", false},
			{"empty_overlap", "ARRAY[1] && '{}'::int[]", false},
			{"both_empty", "'{}'::int[] @> '{}'::int[]", true},
			{"qualified", "ARRAY[1] OPERATOR(pg_catalog.@>) ARRAY[1,1]", true},
			{"function_left", "array_remove(ARRAY[1,2],2) @> ARRAY[1]", true},
			{"function_right", "ARRAY[1] && array_replace(ARRAY[2],2,1)", true},
			{"both_functions", "array_remove(ARRAY[1,2],2) <@ array_replace(ARRAY[2],2,1)", true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				arraySearchBoth(t, p, b, c, fixture.Select("SELECT "+tc.expr+" AS v", []string{"v"}, []string{"BOOL"}, [][]any{{tc.want}}))
			})
		}
		for _, op := range []string{"@>", "<@", "&&"} {
			for _, operands := range [][2]string{{"NULL::int[]", "ARRAY[1]"}, {"ARRAY[1]", "NULL::int[]"}, {"NULL::int[]", "NULL::int[]"}} {
				arraySearchBoth(t, p, b, c, fixture.Select("SELECT "+operands[0]+" "+op+" "+operands[1]+" AS v", []string{"v"}, []string{"BOOL"}, [][]any{{nil}}))
			}
		}
	})
}
