package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONStripNullsConsumers(t *testing.T) {
	jsonStripScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE js_consumer(id int,v json,flag bool)", 0))
		jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO js_consumer VALUES(1,'{"a":null}',false),(2,'{"b":null}',true),(3,'[null,1]',true)`, 3))
		for _, s := range []fixture.Step{
			fixture.Select("SELECT id FROM js_consumer WHERE json_strip_nulls(v,flag)::text='{}' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}),
			fixture.Select("SELECT json_strip_nulls(v,flag)::text AS doc,count(*) AS n FROM js_consumer GROUP BY json_strip_nulls(v,flag)::text ORDER BY doc", []string{"doc", "n"}, []string{"TEXT", "INT8"}, [][]any{{"[1]", int64(1)}, {"{}", int64(2)}}),
			fixture.Select("SELECT json_agg(json_strip_nulls(v,flag) ORDER BY id) AS docs FROM js_consumer", []string{"docs"}, []string{"JSON"}, [][]any{{`[{}, {}, [1]]`}}),
			fixture.Select("SELECT id,(SELECT json_strip_nulls(v,flag)) AS stripped FROM js_consumer ORDER BY id", []string{"id", "stripped"}, []string{"INT4", "JSON"}, [][]any{{int64(1), "{}"}, {int64(2), "{}"}, {int64(3), "[1]"}}),
			jsonStripSelect(`SELECT CASE WHEN true THEN '{}'::json ELSE json_strip_nulls('{"x":"\u0000"}') END AS stripped`, "{}"),
			jsonStripSelect(`SELECT COALESCE('{}'::json,json_strip_nulls('{"x":"\u0000"}')) AS stripped`, "{}"),
		} {
			jsonTypeBoth(t, p, b, c, s)
		}
		jsonTypeBoth(t, p, b, c, jsonStripProcessingError(`SELECT CASE WHEN false THEN '{}'::json ELSE json_strip_nulls('{"x":"\u0000"}') END AS stripped`, "", "22P05", `\u0000 cannot be converted to text.`, `JSON data, line 1: {"x":"\u0000...`, false))
	})
}

func jsonStripMetadata(s fixture.Step, pg bool) fixture.Step {
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
		columns, err := r.Columns()
		if err != nil || !reflect.DeepEqual(columns, []string{"stripped"}) {
			t.Fatal(columns, err)
		}
		types, err := r.ColumnTypes()
		if err != nil || len(types) != 1 {
			t.Fatal(types, err)
		}
		scan := reflect.TypeFor[string]()
		if pg {
			scan = reflect.TypeFor[any]()
		}
		if types[0].Name() != "stripped" || types[0].DatabaseTypeName() != "JSON" || types[0].ScanType() != scan {
			t.Fatal(types[0])
		}
		if _, known := types[0].Length(); known {
			t.Fatal("unexpected JSON length")
		}
		if _, _, known := types[0].DecimalSize(); known {
			t.Fatal("unexpected JSON precision")
		}
		if nullable, known := types[0].Nullable(); nullable || known {
			t.Fatal("computed JSON nullability must be unknown")
		}
	}
	s.CheckScan = func(t *testing.T, r *sql.Rows, i int) {
		var raw any
		if err := r.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		fixture.AssertRawValue(t, pg, "JSON", raw)
		if !reflect.DeepEqual(fixture.Normalize(raw), s.Rows[i][0]) {
			t.Fatalf("raw=%#v want=%#v", raw, s.Rows[i][0])
		}
		var n sql.NullString
		if err := r.Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n.Valid != (s.Rows[i][0] != nil) || n.Valid && n.String != s.Rows[i][0] {
			t.Fatalf("NullString=%#v want=%#v", n, s.Rows[i][0])
		}
		var data []byte
		if err := r.Scan(&data); err != nil {
			t.Fatal(err)
		}
		if s.Rows[i][0] == nil {
			if data != nil {
				t.Fatal(data)
			}
			return
		}
		if string(data) != s.Rows[i][0] {
			t.Fatal(string(data))
		}
		data[0] = 'x'
		var again string
		if err := r.Scan(&again); err != nil || again != s.Rows[i][0] {
			t.Fatal(again, err)
		}
	}
	return s
}

func TestReferenceJSONStripNullsMetadata(t *testing.T) {
	jsonStripScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE js_meta(id int,v json)", 0))
		jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO js_meta VALUES(1,'null'),(2,NULL)`, 2))
		for _, tc := range []struct {
			tail string
			rows [][]any
		}{{" ORDER BY id", [][]any{{"null"}, {nil}}}, {" WHERE id=2", [][]any{{nil}}}, {" WHERE false", nil}} {
			s := jsonStripMetadata(fixture.Select("SELECT json_strip_nulls(v) AS stripped FROM js_meta"+tc.tail, []string{"stripped"}, []string{"JSON"}, tc.rows), b.PG)
			jsonTypeBoth(t, p, b, c, s)
			stmt, err := c.PrepareContext(p.Ctx, s.Query)
			if err != nil {
				t.Fatal(err)
			}
			jsonTypeRun(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: s.Query}, s)
			stmt.Close()
		}
	})
}

func TestReferenceJSONStripNullsStable(t *testing.T) {
	jsonStripScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE js_stable(v json)", 0))
		for _, tc := range []struct{ q, message string }{
			{"CREATE TABLE js_generated(v json,g text GENERATED ALWAYS AS (json_strip_nulls(v)::text) STORED)", "generation expression is not immutable"},
			{"CREATE INDEX js_expr ON js_stable ((json_strip_nulls(v)::text))", "functions in index expression must be marked IMMUTABLE"},
			{"CREATE INDEX js_pred ON js_stable ((v::text)) WHERE json_strip_nulls(v)::text='{}'", "functions in index predicate must be marked IMMUTABLE"},
		} {
			t.Run(tc.message, func(t *testing.T) {
				s := jsonTypeError(tc.q, "42P17", tc.message, 0)
				s.SelectRows = false
				jsonTypeBoth(t, p, b, c, s)
			})
		}
		jsonTypeRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM js_stable", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}
