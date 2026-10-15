package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceArrayRelationalUnnestStorage(t *testing.T) {
	arrayRelationalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT v,n FROM unnest('[-2:-1][5:6]={{1,NULL},{3,4}}'::int[]) WITH ORDINALITY AS u(v,n) ORDER BY n"
		s := fixture.Select(q, []string{"v", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {nil, int64(2)}, {int64(3), int64(3)}, {int64(4), int64(4)}})
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { jsonTypeMetadataCheck(t, rows, b.PG, s.Columns, s.Types) }
		arrayRelationalBoth(t, p, b, c, s)
		for _, q := range []string{
			"SELECT unnest('[-2:-1][5:6]={{1,NULL},{3,4}}'::int[]) AS v",
			"SELECT abs(unnest(ARRAY[-1,NULL,-3,-4])) AS v",
		} {
			s := fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {nil}, {int64(3)}, {int64(4)}})
			s.Bag = true
			arrayRelationalBoth(t, p, b, c, s)
		}
		for _, expr := range []string{"NULL::int[]", "'{}'::int[]"} {
			for _, q := range []string{"SELECT unnest(" + expr + ") AS v", "SELECT * FROM pg_catalog.unnest(" + expr + ") AS u(v)"} {
				s := fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{})
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { jsonTypeMetadataCheck(t, rows, b.PG, s.Columns, s.Types) }
				arrayRelationalBoth(t, p, b, c, s)
			}
		}
		arrayRelationalBoth(t, p, b, c, fixture.Select("SELECT * FROM pg_catalog.unnest(ARRAY[9,NULL]) WITH ORDINALITY AS u(v,n) ORDER BY n", []string{"v", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(9), int64(1)}, {nil, int64(2)}}))
	})
}

func TestReferenceArrayRelationalUnnestZip(t *testing.T) {
	arrayRelationalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			a, z string
			rows [][]any
		}{
			{"ARRAY[1,2]", "ARRAY['a','b','c']", [][]any{{int64(1), "a", int64(1)}, {int64(2), "b", int64(2)}, {nil, "c", int64(3)}}},
			{"ARRAY[1,2,3]", "ARRAY['a']", [][]any{{int64(1), "a", int64(1)}, {int64(2), nil, int64(2)}, {int64(3), nil, int64(3)}}},
			{"'{}'::int[]", "ARRAY['a']", [][]any{{nil, "a", int64(1)}}},
			{"NULL::int[]", "ARRAY['a']", [][]any{{nil, "a", int64(1)}}},
			{"ARRAY[1]", "NULL::text[]", [][]any{{int64(1), nil, int64(1)}}},
			{"ARRAY[1]", "'{}'::text[]", [][]any{{int64(1), nil, int64(1)}}},
			{"NULL::int[]", "'{}'::text[]", [][]any{}},
		} {
			for _, source := range []string{
				"unnest(" + tc.a + "," + tc.z + ")",
				"ROWS FROM (unnest(" + tc.a + "),unnest(" + tc.z + "))",
				"ROWS FROM (unnest(" + tc.a + "," + tc.z + "))",
			} {
				t.Run(source, func(t *testing.T) {
					arrayRelationalBoth(t, p, b, c, fixture.Select("SELECT x,y,n FROM "+source+" WITH ORDINALITY AS u(x,y,n) ORDER BY n", []string{"x", "y", "n"}, []string{"INT4", "TEXT", "INT8"}, tc.rows))
				})
			}
		}
		arrayRelationalBoth(t, p, b, c, fixture.Select("SELECT x,y,z,n FROM unnest(ARRAY[1,2],ARRAY['a'],ARRAY[TRUE,FALSE,TRUE]) WITH ORDINALITY AS u(x,y,z,n) ORDER BY n", []string{"x", "y", "z", "n"}, []string{"INT4", "TEXT", "BOOL", "INT8"}, [][]any{{int64(1), "a", true, int64(1)}, {int64(2), nil, false, int64(2)}, {nil, nil, true, int64(3)}}))
		arrayRelationalBoth(t, p, b, c, fixture.Select("SELECT x,y,z,n FROM ROWS FROM (unnest(ARRAY[1],ARRAY['a','b']),unnest(ARRAY[TRUE])) WITH ORDINALITY AS u(x,y,z,n) ORDER BY n", []string{"x", "y", "z", "n"}, []string{"INT4", "TEXT", "BOOL", "INT8"}, [][]any{{int64(1), "a", true, int64(1)}, {nil, "b", nil, int64(2)}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE ar_mixed(m INT4MULTIRANGE)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ar_mixed VALUES ('{[1,3),[5,8)}')", 1))
		mixed := fixture.Select("SELECT x,r,n FROM ar_mixed,unnest(ARRAY[1,2],m) WITH ORDINALITY AS u(x,r,n) ORDER BY n", []string{"x", "r", "n"}, []string{"INT4", "INT4RANGE", "INT8"}, [][]any{{int64(1), "[1,3)", int64(1)}, {int64(2), "[5,8)", int64(2)}})
		// Ranges have the same exact string/native and nonnil []byte/pq
		// transport as RECORD; keep the logical INT4RANGE descriptor above.
		mixed.RawTypes = []string{"INT4", "RECORD", "INT8"}
		arrayRelationalBoth(t, p, b, c, mixed)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE ar_lateral(id INT,a INT[],b TEXT[])", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ar_lateral VALUES (1,'{1,2}','{a}'),(2,'{}',NULL)", 2))
		arrayRelationalBoth(t, p, b, c, fixture.Select("SELECT id,x,y,n FROM ar_lateral LEFT JOIN LATERAL unnest(a,b) WITH ORDINALITY AS u(x,y,n) ON TRUE ORDER BY id,n", []string{"id", "x", "y", "n"}, []string{"INT4", "INT4", "TEXT", "INT8"}, [][]any{{int64(1), int64(1), "a", int64(1)}, {int64(1), int64(2), nil, int64(2)}, {int64(2), nil, nil, nil}}))
	})
}

func TestReferenceArrayRelationalCompositeElements(t *testing.T) {
	arrayRelationalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"CREATE TYPE ar_pair AS (id INT,label TEXT)", "CREATE DOMAIN ar_pair_domain AS ar_pair"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		for _, typ := range []string{"ar_pair", "ar_pair_domain"} {
			t.Run(typ, func(t *testing.T) {
				expr := "ARRAY[ROW(1,'a')::" + typ + ",NULL::" + typ + ",ROW(3,NULL)::" + typ + "]"
				arrayRelationalBoth(t, p, b, c, fixture.Select("SELECT id,label,n FROM unnest("+expr+") WITH ORDINALITY AS u(id,label,n) ORDER BY n", []string{"id", "label", "n"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(1), "a", int64(1)}, {nil, nil, int64(2)}, {int64(3), nil, int64(3)}}))
				// Named composite scalar DatabaseTypeName is empty on both
				// drivers, independently of the retained logical descriptor.
				s := fixture.Select("SELECT unnest("+expr+") AS v", []string{"v"}, []string{""}, [][]any{{"(1,a)"}, {nil}, {"(3,)"}})
				s.RawTypes = []string{"RECORD"}
				s.Bag = true
				arrayRelationalBoth(t, p, b, c, s)
				for _, empty := range []string{"NULL::" + typ + "[]", "'{}'::" + typ + "[]"} {
					arrayRelationalBoth(t, p, b, c, fixture.Select("SELECT id,label,n FROM unnest("+empty+") WITH ORDINALITY AS u(id,label,n)", []string{"id", "label", "n"}, []string{"INT4", "TEXT", "INT8"}, [][]any{}))
				}
				arrayRelationalBoth(t, p, b, c, fixture.Select("SELECT id,label,x,n FROM unnest("+expr+",ARRAY[10,20,30,40]) WITH ORDINALITY AS u(id,label,x,n) ORDER BY n", []string{"id", "label", "x", "n"}, []string{"INT4", "TEXT", "INT4", "INT8"}, [][]any{{int64(1), "a", int64(10), int64(1)}, {nil, nil, int64(20), int64(2)}, {int64(3), nil, int64(30), int64(3)}, {nil, nil, int64(40), int64(4)}}))
			})
		}
	})
}
