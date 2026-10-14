package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSRFFromValues(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			args, typ string
			values    []int64
		}{
			{"2,4", "INT4", []int64{2, 3, 4}},
			{"1,6,2", "INT4", []int64{1, 3, 5}},
			{"3,3,-2", "INT4", []int64{3}},
			{"3,1", "INT4", nil},
			{"1,3,-1", "INT4", nil},
			{"NULL::integer,3", "INT4", nil},
			{"1,NULL::integer", "INT4", nil},
			{"1,3,NULL::integer", "INT4", nil},
			{"NULL::integer,3,0", "INT4", nil},
			{"1,NULL::integer,0", "INT4", nil},
			{"5::bigint,1::bigint,-2::bigint", "INT8", []int64{5, 3, 1}},
			{"NULL::bigint,3::bigint", "INT8", nil},
			{"2147483646,2147483647,2", "INT4", []int64{2147483646}},
			{"9223372036854775806::bigint,9223372036854775807::bigint,2::bigint", "INT8", []int64{9223372036854775806}},
		} {
			t.Run(tc.args, func(t *testing.T) {
				var rows [][]any
				for i, v := range tc.values {
					rows = append(rows, []any{v, int64(i + 1)})
				}
				seriesBoth(t, p, b, c, fixture.Select("SELECT * FROM generate_series("+tc.args+") WITH ORDINALITY g(v,n) ORDER BY n", []string{"v", "n"}, []string{tc.typ, "INT8"}, rows))
			})
		}
		for _, tc := range []struct {
			args string
			rows [][]any
		}{
			{"'[0:2]={NULL,7,NULL}'::integer[],1", [][]any{{int64(0), int64(1)}, {int64(1), int64(2)}, {int64(2), int64(3)}}},
			{"'[0:2]={NULL,7,NULL}'::integer[],1,true", [][]any{{int64(2), int64(1)}, {int64(1), int64(2)}, {int64(0), int64(3)}}},
			{"'{}'::integer[],1", nil},
			{"NULL::integer[],1", nil},
			{"'{1,2}'::integer[],0", nil},
			{"'{1,2}'::integer[],2", nil},
			{"'{1,2}'::integer[],NULL::integer", nil},
			{"'{1,2}'::integer[],1,NULL::boolean", nil},
		} {
			t.Run(tc.args, func(t *testing.T) {
				seriesBoth(t, p, b, c, fixture.Select("SELECT * FROM generate_subscripts("+tc.args+") WITH ORDINALITY g(v,n) ORDER BY n", []string{"v", "n"}, []string{"INT4", "INT8"}, tc.rows))
			})
		}
		for _, s := range []fixture.Step{
			fixture.Select("SELECT v,n FROM generate_series(5,1,-2) WITH ORDINALITY g(v,n) WHERE v<5 ORDER BY n", []string{"v", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(3), int64(2)}, {int64(1), int64(3)}}),
			fixture.Select("SELECT * FROM generate_series(1,4) WITH ORDINALITY g(v,n) ORDER BY n LIMIT 1 OFFSET 1", []string{"v", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(2), int64(2)}}),
			fixture.Select("SELECT 99 AS x FROM generate_series(3,1)", []string{"x"}, []string{"INT4"}, nil),
			fixture.Select("SELECT * FROM generate_series(1,(SELECT generate_series(2,2))) g(v) ORDER BY v", []string{"v"}, []string{"INT4"}, seriesRows(1, 2)),
		} {
			seriesBoth(t, p, b, c, s)
		}
	})
}

func TestReferenceSRFFromMetadata(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			source      string
			cols, types []string
			rows        [][]any
		}{
			{"generate_series(1,2)", []string{"generate_series"}, []string{"INT4"}, seriesRows(1, 2)},
			{"generate_series(1,2) AS g", []string{"g"}, []string{"INT4"}, seriesRows(1, 2)},
			{"ROWS FROM(generate_series(1,2)) AS g", []string{"g"}, []string{"INT4"}, seriesRows(1, 2)},
			{"generate_series(1,2) WITH ORDINALITY AS g", []string{"g", "ordinality"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}},
			{"generate_series(1,2) WITH ORDINALITY AS g(v)", []string{"v", "ordinality"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}},
			{`generate_series(1,2) WITH ORDINALITY AS "G"("Value","Number")`, []string{"Value", "Number"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}},
			{"generate_series(NULL::bigint,2::bigint) WITH ORDINALITY AS g(v,n)", []string{"v", "n"}, []string{"INT8", "INT8"}, nil},
			{"generate_subscripts('{}'::integer[],1) WITH ORDINALITY AS g", []string{"g", "ordinality"}, []string{"INT4", "INT8"}, nil},
		} {
			t.Run(tc.source, func(t *testing.T) {
				s := fixture.Select("SELECT * FROM "+tc.source+" ORDER BY 1", tc.cols, tc.types, tc.rows)
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					cols, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					if len(cols) != len(tc.cols) {
						t.Fatalf("columns: %v", cols)
					}
					for i, col := range cols {
						if col.Name() != tc.cols[i] || col.DatabaseTypeName() != tc.types[i] {
							t.Fatalf("column %d: %s/%s", i, col.Name(), col.DatabaseTypeName())
						}
					}
				}
				seriesBoth(t, p, b, c, s)
			})
		}
	})
}

func TestReferenceSRFFromComposition(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			fixture.Select("SELECT a.v,b.v FROM generate_series(1,2) a(v),generate_series(10,11) b(v) ORDER BY 1,2", []string{"v", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(1), int64(11)}, {int64(2), int64(10)}, {int64(2), int64(11)}}),
			fixture.Select("SELECT * FROM generate_series(2,1) a(v),generate_series(10,11) b(w)", []string{"v", "w"}, []string{"INT4", "INT4"}, nil),
			fixture.Select("SELECT * FROM generate_series(1,2) a(v),generate_series(11,10) b(w)", []string{"v", "w"}, []string{"INT4", "INT4"}, nil),
			fixture.Select("SELECT * FROM ROWS FROM(generate_series(1,3),generate_series(10::bigint,11::bigint)) WITH ORDINALITY r(a,b,n) ORDER BY n", []string{"a", "b", "n"}, []string{"INT4", "INT8", "INT8"}, [][]any{{int64(1), int64(10), int64(1)}, {int64(2), int64(11), int64(2)}, {int64(3), nil, int64(3)}}),
			fixture.Select("SELECT * FROM ROWS FROM(generate_series(1,1),generate_series(10::bigint,12::bigint)) WITH ORDINALITY r(a,b,n) ORDER BY n", []string{"a", "b", "n"}, []string{"INT4", "INT8", "INT8"}, [][]any{{int64(1), int64(10), int64(1)}, {nil, int64(11), int64(2)}, {nil, int64(12), int64(3)}}),
			fixture.Select("SELECT * FROM ROWS FROM(generate_series(2,1),generate_series(10::bigint,11::bigint)) WITH ORDINALITY r(a,b,n) ORDER BY n", []string{"a", "b", "n"}, []string{"INT4", "INT8", "INT8"}, [][]any{{nil, int64(10), int64(1)}, {nil, int64(11), int64(2)}}),
			fixture.Select("SELECT * FROM ROWS FROM(generate_series(1,2),generate_series(11::bigint,10::bigint)) WITH ORDINALITY r(a,b,n) ORDER BY n", []string{"a", "b", "n"}, []string{"INT4", "INT8", "INT8"}, [][]any{{int64(1), nil, int64(1)}, {int64(2), nil, int64(2)}}),
			fixture.Select("SELECT * FROM ROWS FROM(generate_series(2,1),generate_series(11::bigint,10::bigint)) WITH ORDINALITY r(a,b,n)", []string{"a", "b", "n"}, []string{"INT4", "INT8", "INT8"}, nil),
			fixture.Select("SELECT * FROM ROWS FROM(generate_series(1,2),generate_series(10,10)) ORDER BY 1", []string{"generate_series", "generate_series"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), nil}}),
			fixture.Select("SELECT * FROM ROWS FROM(generate_series(1,3),string_to_table('x,y',','),generate_subscripts('[0:1]={NULL,NULL}'::integer[],1)) WITH ORDINALITY r(v,s,i,n) ORDER BY n", []string{"v", "s", "i", "n"}, []string{"INT4", "TEXT", "INT4", "INT8"}, [][]any{{int64(1), "x", int64(0), int64(1)}, {int64(2), "y", int64(1), int64(2)}, {int64(3), nil, nil, int64(3)}}),
			fixture.Select(`SELECT * FROM ROWS FROM(generate_series(1,3),regexp_split_to_table('a,b',','),json_object_keys('{"x":1,"y":2}'),jsonb_object_keys('{"zz":1,"a":2}')) WITH ORDINALITY r(v,s,j,b,n) ORDER BY n`, []string{"v", "s", "j", "b", "n"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "INT8"}, [][]any{{int64(1), "a", "x", "a", int64(1)}, {int64(2), "b", "y", "zz", int64(2)}, {int64(3), nil, nil, nil, int64(3)}}),
		} {
			t.Run(s.Query, func(t *testing.T) { seriesBoth(t, p, b, c, s) })
		}
	})
}
