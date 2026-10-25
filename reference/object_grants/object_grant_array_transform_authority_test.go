package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantArrayTransformAuthority(t *testing.T) {
	// Targets are released generic formals, never the concrete array used by
	// these calls. Every arity here is a distinct procedure, not a default.
	for _, tc := range []struct {
		name, target, literal, parameter, row string
		args                                  []any
		value, rowNull                        any
		typ                                   string
		strictNull                            []string
		nonstrictNull                         []struct {
			expr string
			want any
		}
		named, operator, qualifiedOperator string
		sibling                            string
		siblingValue                       any
	}{
		{
			name: "append", target: "array_append(anycompatiblearray,anycompatible)",
			literal: "array_append(ARRAY[2,1],3)", parameter: "array_append($1::INT[],$2::INT)", row: "array_append(a,n)",
			args: []any{"{2,1}", int64(3)}, value: "{2,1,3}", rowNull: "{3}", typ: "_INT4",
			nonstrictNull: []struct {
				expr string
				want any
			}{{"array_append(NULL::INT[],3)", "{3}"}, {"array_append(ARRAY[2,1],NULL::INT)", "{2,1,NULL}"}},
			operator: "a || n", qualifiedOperator: "a OPERATOR(pg_catalog.||) n",
			sibling: "array_prepend(n,a)", siblingValue: "{3,2,1}",
		},
		{
			name: "prepend", target: "array_prepend(anycompatible,anycompatiblearray)",
			literal: "array_prepend(3,ARRAY[2,1])", parameter: "array_prepend($1::INT,$2::INT[])", row: "array_prepend(n,a)",
			args: []any{int64(3), "{2,1}"}, value: "{3,2,1}", rowNull: "{3}", typ: "_INT4",
			nonstrictNull: []struct {
				expr string
				want any
			}{{"array_prepend(3,NULL::INT[])", "{3}"}, {"array_prepend(NULL::INT,ARRAY[2,1])", "{NULL,2,1}"}},
			operator: "n || a", qualifiedOperator: "n OPERATOR(pg_catalog.||) a",
			sibling: "array_append(a,n)", siblingValue: "{2,1,3}",
		},
		{
			name: "cat", target: "array_cat(anycompatiblearray,anycompatiblearray)",
			literal: "array_cat(ARRAY[2,1],ARRAY[3])", parameter: "array_cat($1::INT[],$2::INT[])", row: "array_cat(a,z)",
			args: []any{"{2,1}", "{3}"}, value: "{2,1,3}", rowNull: "{3}", typ: "_INT4",
			nonstrictNull: []struct {
				expr string
				want any
			}{{"array_cat(NULL::INT[],ARRAY[3])", "{3}"}, {"array_cat(NULL::INT[],NULL::INT[])", nil}},
			operator: "a || z", qualifiedOperator: "a OPERATOR(pg_catalog.||) z",
			sibling: "array_append(a,n)", siblingValue: "{2,1,3}",
		},
		{
			name: "fill_two", target: "array_fill(anyelement,integer[])",
			literal: "array_fill(3,ARRAY[2])", parameter: "array_fill($1::INT,$2::INT[])", row: "array_fill(x,d)",
			args: []any{int64(3), "{2}"}, value: "{3,3}", rowNull: "{NULL,NULL}", typ: "_INT4",
			nonstrictNull: []struct {
				expr string
				want any
			}{{"array_fill(NULL::INT,ARRAY[2])", "{NULL,NULL}"}},
			sibling: "array_fill(x,d,l)", siblingValue: "[0:1]={3,3}",
		},
		{
			name: "fill_three", target: "array_fill(anyelement,integer[],integer[])",
			literal: "array_fill(3,ARRAY[2],ARRAY[0])", parameter: "array_fill($1::INT,$2::INT[],$3::INT[])", row: "array_fill(x,d,l)",
			args: []any{int64(3), "{2}", "{0}"}, value: "[0:1]={3,3}", rowNull: "[0:1]={NULL,NULL}", typ: "_INT4",
			nonstrictNull: []struct {
				expr string
				want any
			}{{"array_fill(NULL::INT,ARRAY[2],ARRAY[0])", "[0:1]={NULL,NULL}"}},
			sibling: "array_fill(x,d)", siblingValue: "{3,3}",
		},
		{
			name: "reverse", target: "array_reverse(anyarray)",
			literal: "array_reverse(ARRAY[2,1])", parameter: "array_reverse($1::INT[])", row: "array_reverse(a)",
			args: []any{"{2,1}"}, value: "{1,2}", typ: "_INT4", strictNull: []string{"array_reverse(NULL::INT[])"},
			sibling: "array_sort(a)", siblingValue: "{1,2}",
		},
		{
			name: "sort_one", target: "array_sort(anyarray)",
			literal: "array_sort(ARRAY[2,1])", parameter: "array_sort($1::INT[])", row: "array_sort(a)",
			args: []any{"{2,1}"}, value: "{1,2}", typ: "_INT4", strictNull: []string{"array_sort(NULL::INT[])"},
			sibling: "array_sort(a,true)", siblingValue: "{2,1}",
		},
		{
			name: "sort_two", target: "array_sort(anyarray,boolean)",
			literal: "array_sort(ARRAY[2,1],true)", parameter: "array_sort($1::INT[],$2::BOOL)", row: "array_sort(a,f)",
			args: []any{"{2,1}", true}, value: "{2,1}", typ: "_INT4",
			strictNull: []string{"array_sort(NULL::INT[],true)", "array_sort(a,NULL::BOOL)"},
			named:      "array_sort(descending=>f,\"array\"=>a)",
			sibling:    "array_sort(a)", siblingValue: "{1,2}",
		},
		{
			name: "sort_three", target: "array_sort(anyarray,boolean,boolean)",
			literal: "array_sort(ARRAY[2,1],true,false)", parameter: "array_sort($1::INT[],$2::BOOL,$3::BOOL)", row: "array_sort(a,f,g)",
			args: []any{"{2,1}", true, false}, value: "{2,1}", typ: "_INT4",
			strictNull: []string{"array_sort(NULL::INT[],true,false)", "array_sort(a,NULL::BOOL,false)", "array_sort(a,true,NULL::BOOL)"},
			named:      "array_sort(nulls_first=>g,descending=>f,\"array\"=>a)",
			sibling:    "array_sort(a,true)", siblingValue: "{2,1}",
		},
		{
			name: "trim", target: "trim_array(anyarray,integer)",
			literal: "trim_array(ARRAY[2,1],1)", parameter: "trim_array($1::INT[],$2::INT)", row: "trim_array(a,k)",
			args: []any{"{2,1}", int64(1)}, value: "{2}", typ: "_INT4",
			strictNull: []string{"trim_array(NULL::INT[],1)", "trim_array(a,NULL::INT)"},
			sibling:    "array_reverse(a)", siblingValue: "{1,2}",
		},
		{
			name: "string_two", target: "array_to_string(anyarray,text)",
			literal: "array_to_string(ARRAY[2,1],',')", parameter: "array_to_string($1::INT[],$2::TEXT)", row: "array_to_string(a,s)",
			args: []any{"{2,1}", ","}, value: "2,1", typ: "TEXT",
			strictNull: []string{"array_to_string(NULL::INT[],',')", "array_to_string(a,NULL::TEXT)"},
			sibling:    "array_to_string(a,s,m)", siblingValue: "2,1",
		},
		{
			name: "string_three", target: "array_to_string(anyarray,text,text)",
			literal: "array_to_string(ARRAY[2,1],',','*')", parameter: "array_to_string($1::INT[],$2::TEXT,$3::TEXT)", row: "array_to_string(a,s,m)",
			args: []any{"{2,1}", ",", "*"}, value: "2,1", typ: "TEXT",
			nonstrictNull: []struct {
				expr string
				want any
			}{{"array_to_string(NULL::INT[],',','*')", nil}, {"array_to_string(a,NULL::TEXT,'*')", nil}, {"array_to_string(ARRAY[2,NULL,1],',',NULL::TEXT)", "2,1"}},
			sibling: "array_to_string(a,s)", siblingValue: "2,1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_transform(id INT,a INT[],z INT[],n INT,x INT,d INT[],l INT[],f BOOL,g BOOL,k INT,s TEXT,m TEXT)", 0))
				run(fixture.Exec("INSERT INTO og_transform VALUES(1,'{2,1}','{3}',3,3,'{2}','{0}',true,false,1,',','*'),(2,NULL,'{3}',3,NULL,'{2}','{0}',true,false,1,',','*')", 2))
				run(fixture.Exec("GRANT SELECT ON og_transform TO og_object_reader", 0))
				selectValue := func(expr, tail string, want any, args ...any) fixture.Step {
					return fixture.Select("SELECT "+expr+" AS v FROM og_transform"+tail, []string{"v"}, []string{tc.typ}, [][]any{{want}}, args...)
				}
				// View consumers preserve the defining call and use the current
				// invoker's EXECUTE authority, including on an already held SELECT.
				run(fixture.Exec("CREATE VIEW og_transform_view AS SELECT "+tc.row+" AS v FROM og_transform WHERE id=1", 0))
				run(fixture.Exec("GRANT SELECT ON og_transform_view TO og_object_reader", 0))
				// PostgreSQL can retain an already folded IMMUTABLE constant in
				// a held plan after revoke. STABLE to-string calls are not folded.
				holdConstant := tc.typ == "TEXT"
				cases := []objectGrantProviderCase{
					{selectValue(tc.literal, " WHERE id=1", tc.value), true, holdConstant},
					{selectValue("pg_catalog."+tc.literal, " WHERE id=1", tc.value), true, holdConstant},
					{selectValue(tc.parameter, " WHERE id=1", tc.value, tc.args...), true, true},
					{selectValue(tc.row, " WHERE id=1", tc.value), true, true},
					{selectValue(tc.row, " WHERE id=2", tc.rowNull), true, true},
					{fixture.Select("SELECT v FROM og_transform_view", []string{"v"}, []string{tc.typ}, [][]any{{tc.value}}), true, true},
					{fixture.Select("SELECT "+tc.row+" AS v FROM og_transform WHERE id>99", []string{"v"}, []string{tc.typ}, nil), true, true},
					{fixture.Select("SELECT "+tc.row+" AS v FROM og_transform WHERE FALSE", []string{"v"}, []string{tc.typ}, nil), true, true},
					{selectValue("CASE WHEN FALSE THEN "+tc.row+" ELSE NULL END", " WHERE id=1", nil), false, true},
				}
				for _, expr := range tc.strictNull {
					cases = append(cases, objectGrantProviderCase{selectValue(expr, " WHERE id=1", nil), false, true})
				}
				if len(tc.strictNull) != 0 {
					nullArgs := append([]any(nil), tc.args...)
					nullArgs[0] = nil
					// Fresh custom execution may fold a bound whole NULL. Do not
					// assert a generic-plan lifetime promise for this route.
					cases = append(cases, objectGrantProviderCase{selectValue(tc.parameter, " WHERE id=1", nil, nullArgs...), false, false})
				}
				for _, nc := range tc.nonstrictNull {
					cases = append(cases, objectGrantProviderCase{selectValue(nc.expr, " WHERE id=1", nc.want), true, holdConstant})
				}
				if tc.named != "" {
					cases = append(cases, objectGrantProviderCase{selectValue(tc.named, " WHERE id=1", tc.value), true, true})
				}
				if tc.operator != "" {
					for _, expr := range []string{tc.operator, tc.qualifiedOperator} {
						cases = append(cases, objectGrantProviderCase{selectValue(expr, " WHERE id=1", tc.value), true, true})
					}
					run(fixture.Exec("CREATE VIEW og_transform_operator AS SELECT "+tc.operator+" AS v FROM og_transform WHERE id=1", 0))
					run(fixture.Exec("GRANT SELECT ON og_transform_operator TO og_object_reader", 0))
					cases = append(cases, objectGrantProviderCase{fixture.Select("SELECT v FROM og_transform_operator", []string{"v"}, []string{tc.typ}, [][]any{{tc.value}}), true, true})
				}
				control := selectValue(tc.sibling, " WHERE id=1", tc.siblingValue)
				name, _, _ := strings.Cut(tc.target, "(")
				objectGrantProviderLifecycle(t, p, b, c, run, prepared, "pg_catalog."+tc.target, "function", name, cases, []fixture.Step{control})
			})
		})
	}
}
