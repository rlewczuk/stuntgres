package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const arrayTransformMatrix = "'[-1:1][5:6]={{1,2},{3,NULL},{5,6}}'::int[]"

func TestReferenceArrayTransformFirstAxis(t *testing.T) {
	arrayTransformScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		arrayTransformValues(t, p, b, c, []arrayTransformValue{
			{"reverse_rows", "array_reverse(" + arrayTransformMatrix + ")", "_INT4", "[-1:1][5:6]={{5,6},{3,NULL},{1,2}}"},
			{"trim_row", "trim_array(" + arrayTransformMatrix + ",1)", "_INT4", "{{1,2},{3,NULL}}"},
			{"trim_zero_rebases_all_axes", "trim_array(" + arrayTransformMatrix + ",0)", "_INT4", "{{1,2},{3,NULL},{5,6}}"},
			{"trim_all", "trim_array(" + arrayTransformMatrix + ",3)", "_INT4", "{}"},
			{"reverse_empty", "array_reverse('{}'::int[])", "_INT4", "{}"},
			{"reverse_singleton", "array_reverse('[0:0]={1}'::int[])", "_INT4", "[0:0]={1}"},
			{"reverse_null", "array_reverse(NULL::int[])", "_INT4", nil},
			{"trim_null_count", "trim_array(ARRAY[1],NULL)", "_INT4", nil},
			{"trim_null_array", "trim_array(NULL::int[],-1)", "_INT4", nil},
			{"trim_empty", "trim_array('{}'::int[],0)", "_INT4", "{}"},
			{"reverse_json", "array_reverse(ARRAY['1'::json,'2'::json])", "_JSON", "{2,1}"},
			{"trim_json", "trim_array(ARRAY['1'::json,'2'::json],1)", "_JSON", "{1}"},
		})
	})
}

func TestReferenceArrayTransformSort(t *testing.T) {
	arrayTransformScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		arrayTransformValues(t, p, b, c, []arrayTransformValue{
			{"ascending", "array_sort('{3,NULL,1,2,NULL}'::int[])", "_INT4", "{1,2,3,NULL,NULL}"},
			{"descending", "array_sort('{3,NULL,1,2,NULL}'::int[],true)", "_INT4", "{NULL,NULL,3,2,1}"},
			{"descending_null_last", "array_sort('{3,NULL,1,2,NULL}'::int[],true,false)", "_INT4", "{3,2,1,NULL,NULL}"},
			{"ascending_null_first", "array_sort('{3,NULL,1,2,NULL}'::int[],false,true)", "_INT4", "{NULL,NULL,1,2,3}"},
			{"null_descending", "array_sort(ARRAY[1],NULL::bool)", "_INT4", nil},
			{"null_placement", "array_sort(ARRAY[1],false,NULL::bool)", "_INT4", nil},
			{"null_array", "array_sort(NULL::int[])", "_INT4", nil},
			{"empty", "array_sort('{}'::int[])", "_INT4", "{}"},
			{"all_null", "array_sort('{NULL,NULL}'::int[])", "_INT4", "{NULL,NULL}"},
			{"rows", "array_sort('{{2,4},{2,1},{6,5}}'::int[])", "_INT4", "{{2,1},{2,4},{6,5}}"},
			{"bounded_rows", "array_sort('[10:12][20:21]={{1,2},{10,20},{3,4}}'::int[])", "_INT4", "[10:12][20:21]={{1,2},{3,4},{10,20}}"},
			{"inner_null_ascending", "array_sort('{{1,NULL},{1,2},{NULL,0}}'::int[],false,true)", "_INT4", "{{1,2},{1,NULL},{NULL,0}}"},
			{"inner_null_descending", "array_sort('{{1,NULL},{1,2},{NULL,0}}'::int[],true,false)", "_INT4", "{{NULL,0},{1,NULL},{1,2}}"},
			{"json_empty_no_provider", "array_sort('{}'::json[])", "_JSON", "{}"},
			{"json_singleton_no_provider", "array_sort(ARRAY['1'::json])", "_JSON", "{1}"},
			{"collation_c", "array_sort(ARRAY['z','A','a'] COLLATE \"C\")", "_TEXT", "{A,a,z}"},
			{"nan", "array_sort(ARRAY['NaN'::numeric,2::numeric,1::numeric])", "_NUMERIC", "{1,2,NaN}"},
			{"named_two", "array_sort(\"array\" => ARRAY[2,1],descending => false)", "_INT4", "{1,2}"},
			{"named_reordered", "array_sort(nulls_first => false,descending => true,\"array\" => ARRAY[1,NULL,2])", "_INT4", "{2,1,NULL}"},
			{"mixed_named", "array_sort(ARRAY[1,NULL,2],nulls_first => true,descending => false)", "_INT4", "{NULL,1,2}"},
		})
		for _, q := range []string{
			"CREATE DOMAIN at_sort_leaf AS INT",
			"CREATE TYPE at_sort_enum AS ENUM ('second','first')",
			"CREATE TYPE at_sort_pair AS (n INT,s TEXT)",
			"CREATE SEQUENCE at_sort_order",
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		for _, expr := range []string{
			"array_sort(ARRAY[2::at_sort_leaf,1::at_sort_leaf])",
			"array_sort(ARRAY['first'::at_sort_enum,'second'::at_sort_enum])",
			"array_sort(ARRAY[ROW(2,'a')::at_sort_pair,ROW(1,'b')::at_sort_pair])",
			"array_sort(ARRAY[NULL::json,NULL::json])",
			"array_sort('{{NULL},{NULL}}'::json[])",
			"array_sort('{{1},{2}}'::json[])",
			"array_sort('{{NULL,1},{NULL,2}}'::json[])",
			"array_sort(ARRAY['a'] COLLATE \"C\",false)",
		} {
			if expr == "array_sort(ARRAY[ROW(2,'a')::at_sort_pair,ROW(1,'b')::at_sort_pair])" && !b.PG {
				// Use a typed runtime input so Prepare does not fold the
				// immutable call before this execution-stage boundary.
				arrayTransformDiagnostic(t, p, b, c, "SELECT array_sort($1::at_sort_pair[]) AS a FROM at_one", fixture.Diagnostic{Code: "0A000", Message: "composite comparison is unsupported"}, false, `{"(2,a)","(1,b)"}`)
				continue
			}
			arrayTransformAssertContract(t, p, b, c, expr, "SELECT "+expr+" AS a FROM at_one")
		}
		for _, tc := range []struct{ expr, value string }{
			{"array_sort('{}'::at_sort_pair[])", "{}"},
			{"array_sort(ARRAY[ROW(2,'a')::at_sort_pair])", "{\"(2,a)\"}"},
			{"array_reverse(ARRAY[ROW(2,'a')::at_sort_pair,ROW(1,'b')::at_sort_pair])", "{\"(1,b)\",\"(2,a)\"}"},
			{"array_fill(ROW(2,'a')::at_sort_pair,ARRAY[2])", "{\"(2,a)\",\"(2,a)\"}"},
			{"array_append(ARRAY[ROW(2,'a')::at_sort_pair],ROW(1,'b')::at_sort_pair)", "{\"(2,a)\",\"(1,b)\"}"},
			{"array_prepend(ROW(1,'b')::at_sort_pair,ARRAY[ROW(2,'a')::at_sort_pair])", "{\"(1,b)\",\"(2,a)\"}"},
			{"array_cat(ARRAY[ROW(2,'a')::at_sort_pair],ARRAY[ROW(1,'b')::at_sort_pair])", "{\"(2,a)\",\"(1,b)\"}"},
			{"trim_array(ARRAY[ROW(2,'a')::at_sort_pair,ROW(1,'b')::at_sort_pair],1)", "{\"(2,a)\"}"},
			{"array_to_string(ARRAY[ROW(2,'a')::at_sort_pair,ROW(1,'b')::at_sort_pair],'|')", "(2,a)|(1,b)"},
		} {
			typ, raw := "_AT_SORT_PAIR", "_INT4"
			if b.PG {
				typ = ""
			}
			if strings.HasPrefix(tc.expr, "array_to_string(") {
				typ, raw = "TEXT", "TEXT"
			}
			arrayTransformBoth(t, p, b, c, fixture.ArrayReferenceSelect("SELECT "+tc.expr+" AS a FROM at_one", []string{"a"}, []string{typ}, []string{raw}, [][]any{{tc.value}}, b.PG))
		}
		t.Run("composite_held_demand", func(t *testing.T) {
			const q = "SELECT array_sort($1::at_sort_pair[]) AS a FROM at_one"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			typ := "_AT_SORT_PAIR"
			if b.PG {
				typ = ""
			}
			for _, tc := range []struct {
				input, want any
				demand      bool
			}{
				{"{}", "{}", false},
				{`{"(2,a)"}`, `{"(2,a)"}`, false},
				{`{"(2,a)","(1,b)"}`, `{"(1,b)","(2,a)"}`, true},
				{nil, nil, false},
				{`{"(2,a)"}`, `{"(2,a)"}`, false},
			} {
				if tc.demand && !b.PG {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "composite comparison is unsupported"}, tc.input)
					s.SelectRows, s.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					continue
				}
				s := fixture.ArrayReferenceSelect(q, []string{"a"}, []string{typ}, []string{"_INT4"}, [][]any{{tc.want}}, b.PG, tc.input)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
		})
		// Pinned PG18.0 evaluates named arguments in formal order: array
		// consumes 1 and 2 before descending consumes 3 (3%3 -> false),
		// then nulls_first consumes 4 (4%3 -> true). Scalar INT4-to-BOOL
		// casts preserve the existing argument grammar. Reset each route.
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "named_order_direct", true: "named_order_prepared"}[prepared], func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER SEQUENCE at_sort_order RESTART WITH 1", 0))
				q := "SELECT array_sort(descending => (nextval('at_sort_order')::int % 3)::bool,\"array\" => ARRAY[nextval('at_sort_order')::int,nextval('at_sort_order')::int],nulls_first => (nextval('at_sort_order')::int % 3)::bool) AS a FROM at_one"
				s := arrayTransformSelect(b, q, []string{"a"}, []string{"_INT4"}, [][]any{{"{1,2}"}})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT currval('at_sort_order') AS n FROM at_one", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
			})
		}
	})
}

func TestReferenceArrayTransformSerialization(t *testing.T) {
	arrayTransformScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		arrayTransformValues(t, p, b, c, []arrayTransformValue{
			{"flatten_omit", "array_to_string('{{1,NULL},{2,3}}'::int[],',')", "TEXT", "1,2,3"},
			{"flatten_marker", "array_to_string('{{1,NULL},{2,3}}'::int[],',','*')", "TEXT", "1,*,2,3"},
			{"null_marker_omits", "array_to_string('{{1,NULL},{2,3}}'::int[],',',NULL)", "TEXT", "1,2,3"},
			{"empty", "array_to_string('{}'::int[],',')", "TEXT", ""},
			{"all_null", "array_to_string('{NULL,NULL}'::int[],',')", "TEXT", ""},
			{"empty_marker_emits", "array_to_string('{NULL,NULL}'::int[],',','')", "TEXT", ","},
			{"empty_nonnull_emits", "array_to_string(ARRAY['',NULL,''],',')", "TEXT", ","},
			{"empty_delimiter", "array_to_string(ARRAY[1,2,3],'')", "TEXT", "123"},
			{"null_array", "array_to_string(NULL::int[],',')", "TEXT", nil},
			{"null_delimiter", "array_to_string(ARRAY[1],NULL)", "TEXT", nil},
			{"null_array_three", "array_to_string(NULL::int[],',','*')", "TEXT", nil},
			{"null_delimiter_three", "array_to_string(ARRAY[1],NULL,'*')", "TEXT", nil},
			{"bool_output_not_driver", "array_to_string(ARRAY[true,false,NULL],'|','N')", "TEXT", "t|f|N"},
			{"raw_text_no_array_quoting", "array_to_string(ARRAY['a,b','\"quoted\"',E'c\\\\d'],'|')", "TEXT", "a,b|\"quoted\"|c\\d"},
			{"bounds_ignored", "array_to_string('[0:1][5:6]={{1,NULL},{2,3}}'::int[],',','*')", "TEXT", "1,*,2,3"},
		})
		for _, q := range []string{
			"CREATE DOMAIN at_output_vector AS INT[]",
			"CREATE TABLE at_output_catalog(id INT)",
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		arrayTransformAssertContract(t, p, b, c, "array_domain_leaf", "SELECT array_to_string(ARRAY['{1,2}'::at_output_vector,'{3}'::at_output_vector],'|') AS a FROM at_one")
		for _, setting := range []struct{ sql, expr string }{
			{"SET bytea_output='hex'", "array_to_string(ARRAY['\\x0001ff'::bytea],'|')"},
			{"SET bytea_output='escape'", "array_to_string(ARRAY['\\x0001ff'::bytea],'|')"},
			{"SET DateStyle='ISO, MDY'", "array_to_string(ARRAY['2020-02-03'::date],',')"},
			{"SET DateStyle='SQL, DMY'", "array_to_string(ARRAY['2020-02-03'::date],',')"},
			{"SET TimeZone='UTC'", "array_to_string(ARRAY['2020-02-03 04:05:06+00'::timestamptz],',')"},
			{"SET TimeZone='America/New_York'", "array_to_string(ARRAY['2020-02-03 04:05:06+00'::timestamptz],',')"},
			{"SET IntervalStyle='postgres'", "array_to_string(ARRAY['1 day 02:03:04'::interval],',')"},
			{"SET IntervalStyle='iso_8601'", "array_to_string(ARRAY['1 day 02:03:04'::interval],',')"},
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(setting.sql, 0))
			arrayTransformAssertContract(t, p, b, c, setting.sql, "SELECT "+setting.expr+" AS a FROM at_one")
		}
		q := "SELECT array_to_string($1::regclass[],',') AS a FROM at_one"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, name := range []string{"at_output_catalog", "at_output_renamed"} {
			if name == "at_output_renamed" {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER TABLE at_output_catalog RENAME TO at_output_renamed", 0))
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"a"}, []string{"TEXT"}, [][]any{{name}}, "{"+name+"}"))
		}
		arrayTransformHeldOutputEnvironment(t, p, b, c)
	})
}
