package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBEachQueryStructure(t *testing.T) {
	jsonbEachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, f := range []string{"jsonb_each", "jsonb_each_text"} {
			t.Run(f, func(t *testing.T) {
				valueType := jsonbEachType(f)
				for _, q := range []string{
					"SELECT key,value FROM " + f + "('{\"a\":1}'::jsonb)",
					"SELECT j.key,j.value FROM " + f + "('{\"a\":1}'::jsonb) AS j",
					"SELECT (j).key,(j).value FROM " + f + "('{\"a\":1}'::jsonb) AS j",
					"SELECT (" + f + "('{\"a\":1}'::jsonb)).key AS key,(" + f + "('{\"a\":1}'::jsonb)).value AS value",
				} {
					fixture.JSONEachBoth(t, p, b, c, jsonbEachSelect(f, q, [][]any{{"a", "1"}}))
				}
				fixture.JSONEachBoth(t, p, b, c, fixture.Select("SELECT j FROM "+f+"('{\"a\":1}'::jsonb) AS j", []string{"j"}, []string{"RECORD"}, [][]any{{"(a,1)"}}))
				for _, input := range []string{`{"a":1,"b":2}`, `{}`, `null`} {
					arg := "'" + input + "'::jsonb"
					if input == "null" {
						arg = "NULL::jsonb"
					}
					var rows [][]any
					if input == `{"a":1,"b":2}` {
						rows = [][]any{{"a", "1", int64(7), int64(1)}, {"b", "2", int64(8), int64(2)}, {nil, nil, int64(9), int64(3)}}
					} else {
						rows = [][]any{{nil, nil, int64(7), int64(1)}, {nil, nil, int64(8), int64(2)}, {nil, nil, int64(9), int64(3)}}
					}
					q := "SELECT * FROM ROWS FROM(" + f + "(" + arg + "),generate_series(7::int,9::int)) WITH ORDINALITY AS r(k,v,g,n) ORDER BY n"
					fixture.JSONEachBoth(t, p, b, c, jsonEachWithMetadata(fixture.Select(q, []string{"k", "v", "g", "n"}, []string{"TEXT", valueType, "INT4", "INT8"}, rows), b.PG))
				}
				nullValue := any("null")
				if f == "jsonb_each_text" {
					nullValue = nil
				}
				for _, tc := range []struct {
					q            string
					names, types []string
					rows         [][]any
				}{
					{"SELECT * FROM ROWS FROM(generate_series(7::int,8::int)," + f + "('{\"a\":null}')) WITH ORDINALITY AS r(g,k,v,n) ORDER BY n", []string{"g", "k", "v", "n"}, []string{"INT4", "TEXT", valueType, "INT8"}, [][]any{{int64(7), "a", nullValue, int64(1)}, {int64(8), nil, nil, int64(2)}}},
					{"SELECT * FROM ROWS FROM(" + f + "('{\"a\":null}')," + f + "('{\"x\":2,\"y\":3}')) WITH ORDINALITY AS r(k,v,k2,v2,n) ORDER BY n", []string{"k", "v", "k2", "v2", "n"}, []string{"TEXT", valueType, "TEXT", valueType, "INT8"}, [][]any{{"a", nullValue, "x", "2", int64(1)}, {nil, nil, "y", "3", int64(2)}}},
					{"SELECT * FROM ROWS FROM(" + f + "('{}')," + f + "(NULL)) AS r(k,v,k2,v2)", []string{"k", "v", "k2", "v2"}, []string{"TEXT", valueType, "TEXT", valueType}, nil},
					{"SELECT a.key AS k,b.key AS k2 FROM " + f + "('{\"a\":1,\"b\":2}') a CROSS JOIN " + f + "('{\"x\":3,\"y\":4}') b ORDER BY k,k2", []string{"k", "k2"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "x"}, {"a", "y"}, {"b", "x"}, {"b", "y"}}},
					{"SELECT " + f + "('{\"a\":1}') AS r,generate_series(7::int,8::int) AS g", []string{"r", "g"}, []string{"RECORD", "INT4"}, [][]any{{"(a,1)", int64(7)}, {nil, int64(8)}}},
					{"SELECT upper(key) AS k FROM (SELECT key FROM " + f + "('{\"a\":1}')) s", []string{"k"}, []string{"TEXT"}, [][]any{{"A"}}},
					{"WITH s AS (SELECT key FROM " + f + "('{\"b\":1,\"a\":2,\"a\":3}')) SELECT key,count(*) AS n FROM s GROUP BY key ORDER BY key", []string{"key", "n"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(1)}, {"b", int64(1)}}},
					{"SELECT DISTINCT key FROM " + f + "('{\"b\":1,\"a\":2,\"a\":3}') ORDER BY key", []string{"key"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}}},
					{"SELECT " + f + "('{\"a\":1}') AS r," + f + "('{\"x\":2,\"y\":3}') AS r2,99 AS tail", []string{"r", "r2", "tail"}, []string{"RECORD", "RECORD", "INT4"}, [][]any{{"(a,1)", "(x,2)", int64(99)}, {nil, "(y,3)", int64(99)}}},
					{"SELECT r FROM ROWS FROM(" + f + "('{\"a\":1}'),generate_series(7::int,7::int)) AS r", []string{"r"}, []string{"RECORD"}, [][]any{{"(a,1,7)"}}},
					{"SELECT a.key AS k,b.key AS k2 FROM " + f + "('{\"a\":1,\"b\":2}') a," + f + "('{\"x\":3,\"y\":4}') b ORDER BY k,k2", []string{"k", "k2"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "x"}, {"a", "y"}, {"b", "x"}, {"b", "y"}}},
				} {
					t.Run(tc.q, func(t *testing.T) {
						s := fixture.Select(tc.q, tc.names, tc.types, tc.rows)
						s.Bag = !strings.Contains(tc.q, "ORDER BY")
						fixture.JSONEachBoth(t, p, b, c, s)
					})
				}
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE jbeach_lateral(id int,j jsonb)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO jbeach_lateral VALUES(1,'{"a":1,"b":2}'),(2,'{}'),(3,NULL)`, 3))
		for _, f := range []string{"jsonb_each", "jsonb_each_text"} {
			for _, from := range []string{"jbeach_lateral s," + f + "(s.j) f", "jbeach_lateral s CROSS JOIN LATERAL " + f + "(s.j) f"} {
				fixture.JSONEachBoth(t, p, b, c, fixture.Select("SELECT s.id,f.key,f.value FROM "+from+" ORDER BY s.id,f.key", []string{"id", "key", "value"}, []string{"INT4", "TEXT", jsonbEachType(f)}, [][]any{{int64(1), "a", "1"}, {int64(1), "b", "2"}}))
			}
			fixture.JSONEachBoth(t, p, b, c, fixture.Select("SELECT s.id,f.key,f.value FROM jbeach_lateral s LEFT JOIN LATERAL "+f+"(s.j) f ON true ORDER BY s.id,f.key", []string{"id", "key", "value"}, []string{"INT4", "TEXT", jsonbEachType(f)}, [][]any{{int64(1), "a", "1"}, {int64(1), "b", "2"}, {int64(2), nil, nil}, {int64(3), nil, nil}}))
			fixture.JSONEachBoth(t, p, b, c, fixture.Select("SELECT s.id,f.key FROM jbeach_lateral s,"+f+"((SELECT s.j)) f ORDER BY s.id,f.key", []string{"id", "key"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "b"}}))
		}
	})
}
