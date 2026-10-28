package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONBPathDeleteTyping(t *testing.T) {
	jsonbPathDeleteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			q    string
			want any
			args []any
		}{
			{`SELECT '{"a":1}' #- '{a}' AS v`, `{}`, nil},
			{`SELECT $1 #- $2 AS v`, `{}`, []any{`{"a":1}`, `{a}`}},
			{`SELECT NULL #- NULL AS v`, nil, nil},
			{`SELECT '{"a":1}'::jsonb OPERATOR(pg_catalog.#-) ARRAY['a'] AS v`, `{}`, nil},
			{`SELECT (jsonb_set('{}','{a}','1') #- '{a}') AS v`, `{}`, nil},
			{`SELECT '{"a":1}'::jsonb #- string_to_array('a', ',') AS v`, `{}`, nil},
			{`SELECT jsonb_set('{}','{a}','1') #- string_to_array('a', ',') AS v`, `{}`, nil},
			{`SELECT '{"a":1,"b":2}'::jsonb #- '{a}' #- '{b}' AS v`, `{}`, nil},
			{`SELECT ('{"a":1,"b":2}'::jsonb #- '{a}') -> 'b' AS v`, `2`, nil},
			{`SELECT ('{"a":1,"b":2}'::jsonb OPERATOR(pg_catalog.#-) '{a}') OPERATOR(pg_catalog.#-) '{b}' AS v`, `{}`, nil},
			{`SELECT '{"a":1}'::jsonb #-/* boundary */'{a}' AS v`, `{}`, nil},
		} {
			t.Run(tc.q, func(t *testing.T) { jsonTypeBoth(t, p, b, c, jsonbPathDeleteSelect(tc.q, tc.want, tc.args...)) })
		}
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE DOMAIN jbd_doc AS jsonb", 0))
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE DOMAIN jbd_path AS text[]", 0))
		jsonTypeBoth(t, p, b, c, jsonbPathDeleteSelect(`SELECT '{"a":1}'::jbd_doc #- '{a}'::jbd_path AS v`, `{}`))
	})
}

func jsonbPathDeleteMetadata(s fixture.Step, pg bool) fixture.Step {
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) { fixture.JSONBObjectMetadata(t, r, pg, "v") }
	s.CheckScan = func(t *testing.T, r *sql.Rows, i int) {
		want := s.Rows[i][0]
		var raw any
		if err := r.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		fixture.AssertRawValue(t, pg, "JSONB", raw)
		if !reflect.DeepEqual(fixture.Normalize(raw), want) {
			t.Fatalf("raw=%#v want=%#v", raw, want)
		}
		var n sql.NullString
		if err := r.Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n.Valid != (want != nil) || (n.Valid && n.String != want) {
			t.Fatalf("NullString=%#v want=%#v", n, want)
		}
		var data []byte
		if err := r.Scan(&data); err != nil {
			t.Fatal(err)
		}
		if want == nil {
			if data != nil {
				t.Fatal(data)
			}
			return
		}
		if string(data) != want {
			t.Fatalf("bytes=%q want=%q", data, want)
		}
		data[0] = 'x'
		var text string
		if err := r.Scan(&text); err != nil || text != want {
			t.Fatalf("detached=%q want=%q err=%v", text, want, err)
		}
	}
	return s
}

func TestReferenceJSONBPathDeleteMetadata(t *testing.T) {
	jsonbPathDeleteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE jbd_meta(id int,doc jsonb,path text[])", 0))
		jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO jbd_meta VALUES(1,'{"a":1}','{a}'),(2,NULL,'{a}')`, 2))
		for _, tc := range []struct {
			suffix string
			rows   [][]any
		}{
			{" ORDER BY id", [][]any{{`{}`}, {nil}}}, {" WHERE id=2", [][]any{{nil}}}, {" WHERE id=99", nil},
		} {
			q := "SELECT doc #- path AS v FROM jbd_meta" + tc.suffix
			s := jsonbPathDeleteMetadata(fixture.Select(q, []string{"v"}, []string{"JSONB"}, tc.rows), b.PG)
			jsonTypeBoth(t, p, b, c, s)
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
			stmt.Close()
		}
	})
}
