package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBRecordDefinitions(t *testing.T) {
	jsonbRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonbRecordSetup(t, p, b, c, `CREATE TYPE jbr_def_pair AS (a int,b text)`, `CREATE TYPE jbr_mood AS ENUM ('ok','sad')`, `CREATE DOMAIN jbr_short AS varchar(3)`)
		for _, source := range []string{
			`jsonb_to_record($1::jsonb) AS (a int,b text,c numeric(4,1))`,
			`jsonb_to_record($1::jsonb) r(a int,b text,c numeric(4,1))`,
			`jsonb_to_record($1::jsonb) AS r(a int,b text,c numeric(4,1))`,
			`ROWS FROM(jsonb_to_record($1::jsonb) AS (a int,b text,c numeric(4,1))) r`,
			`jsonb_populate_record(NULL::record,$1::jsonb) AS r(a int,b text,c numeric(4,1))`,
		} {
			for _, tc := range []struct {
				input any
				row   []any
			}{
				{`{"a":1,"b":"two","c":12.34}`, []any{int64(1), "two", "12.3"}},
				{`{}`, []any{nil, nil, nil}},
				{nil, []any{nil, nil, nil}},
			} {
				t.Run(source+":"+stringValue(tc.input), func(t *testing.T) {
					jsonEachBoth(t, p, b, c, fixture.Select("SELECT * FROM "+source, []string{"a", "b", "c"}, []string{"INT4", "TEXT", "NUMERIC"}, [][]any{tc.row}, tc.input))
				})
			}
		}
		jsonEachBoth(t, p, b, c, fixture.Select(`SELECT "A",m::text AS m,s::text AS s,(r).b AS b FROM jsonb_to_record($1::jsonb) AS x("A" int,m public.jbr_mood,s public.jbr_short,r public.jbr_def_pair)`, []string{"A", "m", "s", "b"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(7), "ok", "abc", "nested"}}, `{"A":7,"m":"ok","s":"abc","r":{"b":"nested"}}`))
		jsonEachBoth(t, p, b, c, fixture.Select(`SELECT s FROM jsonb_to_record('{"s":"é"}'::jsonb) AS r(s text COLLATE "C")`, []string{"s"}, []string{"TEXT"}, [][]any{{"é"}}))
		jsonEachBoth(t, p, b, c, fixture.Select(`SELECT a,b,c,n FROM ROWS FROM(jsonb_to_recordset($1::jsonb) AS (a int,b text,c numeric(4,1))) WITH ORDINALITY AS r(a,b,c,n) ORDER BY n`, []string{"a", "b", "c", "n"}, []string{"INT4", "TEXT", "NUMERIC", "INT8"}, [][]any{{int64(1), "x", "12.3", int64(1)}}, `[{"a":1,"b":"x","c":12.34}]`))
	})
}

// A stable label for bound-input subtests, not SQL conversion.
func stringValue(v any) string {
	if v == nil {
		return "SQLNULL"
	}
	return v.(string)
}

func TestReferenceJSONBRecordSets(t *testing.T) {
	jsonbRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonbRecordSetup(t, p, b, c, `CREATE TYPE jbr_set_pair AS (a int,b text)`, `CREATE TABLE jbr_set_docs(id int,j jsonb)`)
		for _, input := range []any{`[{"a":1},{"b":"x"},{}]`, `[]`, nil} {
			var want [][]any
			var scalar [][]any
			if input != nil && input != `[]` {
				want = [][]any{{int64(1), "keep", int64(1)}, {int64(9), "x", int64(2)}, {int64(9), "keep", int64(3)}}
				scalar = [][]any{{"(1,keep)"}, {"(9,x)"}, {"(9,keep)"}}
			}
			jsonEachBoth(t, p, b, c, fixture.Select(`SELECT * FROM jsonb_populate_recordset('(9,keep)'::jbr_set_pair,$1::jsonb) WITH ORDINALITY AS r(a,b,n) ORDER BY n`, []string{"a", "b", "n"}, []string{"INT4", "TEXT", "INT8"}, want, input))
			jsonbRecordCompositeBoth(t, p, b, c, fixture.Select(`SELECT jsonb_populate_recordset('(9,keep)'::jbr_set_pair,$1::jsonb) AS v`, []string{"v"}, []string{""}, scalar, input))
		}
		jsonEachBoth(t, p, b, c, fixture.Select(`SELECT (jsonb_populate_recordset(NULL::jbr_set_pair,'[{"a":1},{"a":2}]'::jsonb)).a AS a`, []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		for _, input := range []any{`[]`, nil} {
			jsonEachBoth(t, p, b, c, fixture.Select(`SELECT * FROM jsonb_to_recordset($1::jsonb) AS r(a int,b text)`, []string{"a", "b"}, []string{"INT4", "TEXT"}, nil, input))
		}
		jsonEachBoth(t, p, b, c, fixture.Select(`SELECT * FROM ROWS FROM(jsonb_to_recordset('[{"a":1}]'::jsonb) AS (a int),generate_series(7,9)) WITH ORDINALITY AS r(a,g,n) ORDER BY n`, []string{"a", "g", "n"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(7), int64(1)}, {nil, int64(8), int64(2)}, {nil, int64(9), int64(3)}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO jbr_set_docs VALUES(1,'[{"a":1},{"a":2}]'),(2,'[]'),(3,NULL),(4,'[{"a":4}]')`, 4))
		jsonEachBoth(t, p, b, c, fixture.Select(`SELECT d.id,r.a,r.n FROM jbr_set_docs d LEFT JOIN LATERAL ROWS FROM(jsonb_to_recordset(d.j) AS (a int)) WITH ORDINALITY AS r(a,n) ON true ORDER BY d.id,r.n`, []string{"id", "a", "n"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(1), int64(1)}, {int64(1), int64(2), int64(2)}, {int64(2), nil, nil}, {int64(3), nil, nil}, {int64(4), int64(4), int64(1)}}))
		jsonEachBoth(t, p, b, c, fixture.Select(`SELECT d.id,r.a FROM jbr_set_docs d,jsonb_to_recordset(d.j) AS r(a int) ORDER BY d.id,r.a`, []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(1), int64(2)}, {int64(4), int64(4)}}))
		for _, suffix := range []string{" LIMIT 1", " LIMIT 0", " WHERE false"} {
			t.Run("demand"+suffix, func(t *testing.T) {
				q := `SELECT * FROM jsonb_to_recordset('[{"a":1},{"a":"bad"}]'::jsonb) AS r(a int)` + suffix
				if suffix == " LIMIT 1" {
					fixture.CompositeReferenceError(t, p, c, q, "execute", fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`))
				} else {
					jsonEachBoth(t, p, b, c, fixture.Select(q, []string{"a"}, []string{"INT4"}, nil))
				}
			})
		}
		t.Run("exists late failure", func(t *testing.T) {
			fixture.CompositeReferenceError(t, p, c, `SELECT EXISTS(SELECT * FROM jsonb_to_recordset('[{"a":1},null]'::jsonb) AS r(a int))`, "execute", fixture.ControlsReferenceDiagnostic("22023", "argument of jsonb_to_recordset must be an array of objects"))
		})
	})
}
