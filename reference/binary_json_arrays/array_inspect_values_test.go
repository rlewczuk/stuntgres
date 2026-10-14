package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceArrayInspectValues(t *testing.T) {
	arrayInspectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			name, a, d string
			want       []any
		}{
			{"ordinary", "'{1,NULL,3}'::int[]", "1", []any{"[1:3]", int64(3), int64(1), int64(3), int64(1), int64(3)}},
			{"all_null_elements", "'{NULL,NULL}'::int[]", "1", []any{"[1:2]", int64(2), int64(1), int64(2), int64(1), int64(2)}},
			{"rank2_first", "'" + arrayInspectInput + "'::int[]", "1", []any{"[-2:-1][3:5]", int64(2), int64(-2), int64(-1), int64(2), int64(6)}},
			{"rank2_second", "'" + arrayInspectInput + "'::int[]", "2", []any{"[-2:-1][3:5]", int64(3), int64(3), int64(5), int64(2), int64(6)}},
			{"zero_bound", "'[0:1][-1:0]={{1,2},{3,4}}'::int[]", "1", []any{"[0:1][-1:0]", int64(2), int64(0), int64(1), int64(2), int64(4)}},
			{"constructor", "ARRAY[[1,NULL],[3,4]]", "2", []any{"[1:2][1:2]", int64(2), int64(1), int64(2), int64(2), int64(4)}},
			{"empty", "'{}'::int[]", "1", []any{nil, nil, nil, nil, nil, int64(0)}},
			{"nested_empty", "'{{},{}}'::int[]", "1", []any{nil, nil, nil, nil, nil, int64(0)}},
			{"whole_null", "NULL::int[]", "1", []any{nil, nil, nil, nil, nil, nil}},
			{"maximum_bound", "'[2147483646:2147483646]={7}'::int[]", "1", []any{"[2147483646:2147483646]", int64(1), int64(2147483646), int64(2147483646), int64(1), int64(1)}},
			{"minimum_bound", "'[-2147483648:-2147483648]={7}'::int[]", "1", []any{"[-2147483648:-2147483648]", int64(1), int64(-2147483648), int64(-2147483648), int64(1), int64(1)}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				arrayInspectBoth(t, p, b, c, arrayInspectSelect("SELECT "+arrayInspectTuple(tc.a, tc.d), [][]any{tc.want}, b.PG))
			})
		}
		for _, d := range []string{"0", "-1", "3", "2147483647", "(-2147483648)::int", "NULL::int"} {
			t.Run("missing_dimension_"+d, func(t *testing.T) {
				arrayInspectBoth(t, p, b, c, arrayInspectSelect("SELECT "+arrayInspectTuple("'"+arrayInspectInput+"'::int[]", d), [][]any{{"[-2:-1][3:5]", nil, nil, nil, int64(2), int64(6)}}, b.PG))
			})
		}
		for _, q := range []string{"CREATE DOMAIN ai_element AS INT", "CREATE DOMAIN ai_outer AS INT[]", "CREATE TYPE ai_enum AS ENUM ('red','blue')"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		for _, a := range []string{
			"'{1,NULL}'::ai_outer", "'{1,NULL}'::ai_element[]", "ARRAY[1::ai_element,NULL::ai_element]",
			`'{"{1,2}",NULL}'::ai_outer[]`, "ARRAY['x'::name,NULL::name]", "ARRAY['{}'::json,NULL::json]",
			"ARRAY['x'::text,NULL::text]", "ARRAY['red'::ai_enum,NULL::ai_enum]",
		} {
			t.Run(a, func(t *testing.T) {
				arrayInspectBoth(t, p, b, c, arrayInspectSelect("SELECT "+arrayInspectTuple(a, "1"), [][]any{{"[1:2]", int64(2), int64(1), int64(2), int64(1), int64(2)}}, b.PG))
			})
		}
	})
}

func TestReferenceArrayInspectTyping(t *testing.T) {
	arrayInspectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"CREATE DOMAIN ai_dimension AS INT", "CREATE TABLE ai_catalog_probe(a INT,b TEXT)"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		for _, name := range []string{"array_length", "ArRaY_LeNgTh", `"array_length"`, "pg_catalog.array_length"} {
			for _, d := range []string{"2::smallint", "2::ai_dimension", "'2'"} {
				arrayInspectBoth(t, p, b, c, fixture.Select("SELECT "+name+"('"+arrayInspectInput+"'::int[],"+d+") AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(3)}}))
			}
		}
		for _, tc := range []struct {
			expr, typ string
			value     any
		}{
			{"array_dims(VARIADIC ARRAY[1,2])", "TEXT", "[1:2]"},
			{"cardinality(VARIADIC ARRAY[1,2])", "INT4", int64(2)},
			{"array_length(ARRAY[1,2],VARIADIC 1)", "INT4", int64(2)},
			{"array_dims(ALL ARRAY[1,2])", "TEXT", "[1:2]"},
		} {
			arrayInspectBoth(t, p, b, c, fixture.Select("SELECT "+tc.expr+" AS v", []string{"v"}, []string{tc.typ}, [][]any{{tc.value}}))
		}
		arrayInspectBoth(t, p, b, c, fixture.Select("SELECT cardinality,array_dims FROM (VALUES(7,'column')) AS v(cardinality,array_dims)", []string{"cardinality", "array_dims"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "column"}}))
		arrayInspectBoth(t, p, b, c, arrayInspectSelect("SELECT "+arrayInspectTuple("attmissingval", "1")+" FROM pg_catalog.pg_attribute WHERE attrelid='ai_catalog_probe'::regclass::oid AND attnum>0 ORDER BY attnum", [][]any{{nil, nil, nil, nil, nil, nil}, {nil, nil, nil, nil, nil, nil}}, b.PG))
	})
}
