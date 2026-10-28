package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSQLJSONFeatureSmoke(t *testing.T) {
	sqljsonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE sqj_smoke(id int,doc text)`, 0))
		run(fixture.Exec(`INSERT INTO sqj_smoke VALUES(1,'{"n":1}'),(2,NULL)`, 2))
		insert, err := c.PrepareContext(p.Ctx, `INSERT INTO sqj_smoke VALUES($1::int,$2::text)`)
		if err != nil {
			t.Fatal(err)
		}
		defer insert.Close()
		affected, err := insert.ExecContext(p.Ctx, int64(3), `{"n":3}`)
		if err != nil {
			t.Fatal(err)
		}
		count, err := affected.RowsAffected()
		if err != nil || count != 1 {
			t.Fatalf("held insert count=%d err=%v", count, err)
		}
		if err = insert.Close(); err != nil {
			t.Fatal(err)
		}
		fixture.SQLJSONBoth(t, p, b, c, fixture.Select(`SELECT JSON_OBJECT('a' VALUE JSON_ARRAY(1,NULL,'null' FORMAT JSON),'b':JSON_ARRAY(SELECT doc FROM sqj_smoke WHERE id<=2 ORDER BY id FORMAT JSON) WITH UNIQUE KEYS) AS v`, []string{"v"}, []string{"JSON"}, [][]any{{`{"a" : [1, null], "b" : [{"n":1}]}`}}))
		fixture.SQLJSONBoth(t, p, b, c, fixture.Select(`SELECT id,JSON_ARRAY(SELECT x.id FROM sqj_smoke x WHERE x.id<=s.id ORDER BY x.id RETURNING text) AS a FROM sqj_smoke s ORDER BY id`, []string{"id", "a"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), `[1]`}, {int64(2), `[1, 2]`}, {int64(3), `[1, 2, 3]`}}))
		sqljsonValues(t, p, b, c, []struct {
			expr, typ string
			want      any
		}{
			{`JSON_ARRAY(SELECT id FROM sqj_smoke WHERE FALSE)`, "JSON", nil},
			{`JSON_ARRAY(SELECT doc FROM sqj_smoke WHERE id=2)`, "JSON", `[]`},
			{`JSON_ARRAY('{}'::jsonb)`, "JSONB", `[{}]`},
			{`JSON_ARRAY('{}'::jsonb RETURNING json FORMAT JSON)`, "JSON", `[{}]`},
			{`JSON_ARRAY(1,NULL NULL ON NULL RETURNING bytea)`, "BYTEA", `[1, null]`},
			{`JSON_OBJECT('a':NULL ABSENT ON NULL WITH UNIQUE KEYS)`, "JSON", `{}`},
			{`JSON(' {"n":1} ')`, "JSON", ` {"n":1} `},
			{`JSON_SCALAR(1)`, "JSON", `1`}, {`JSON_SCALAR('x')`, "JSON", `"x"`},
			{`JSON_SCALAR(NULL::int)`, "JSON", nil}, {`JSON_SCALAR(ARRAY[1,NULL])`, "JSON", `[1,null]`},
			{`JSON_SERIALIZE('[1]' RETURNING bytea)`, "BYTEA", `[1]`},
			{`'[ {"a":1,"a":2} ]' IS JSON WITH UNIQUE KEYS`, "BOOL", false},
			{`NULL::text IS NOT JSON`, "BOOL", nil}, {`'x' IS JSON`, "BOOL", false},
			{`JSON_OBJECT(ARRAY['a','1'])`, "JSON", `{"a" : "1"}`},
		})
		for _, expr := range []string{`JSON_SERIALIZE(' {"b":2,"a":1} '::jsonb)`, `JSON_SERIALIZE(' {"b":2,"a":1} '::jsonb RETURNING text)`, `JSON_SERIALIZE(' {"b":2,"a":1} '::jsonb FORMAT JSON RETURNING text FORMAT JSON)`} {
			want := `{"a": 1, "b": 2}`
			if b.PG {
				want = "\x02"
			}
			fixture.SQLJSONBoth(t, p, b, c, fixture.Select(`SELECT `+expr+` AS v`, []string{"v"}, []string{"TEXT"}, [][]any{{want}}))
		}
		for _, root := range []struct{ source, kind string }{{`{}`, "OBJECT"}, {`[]`, "ARRAY"}, {`"s"`, "SCALAR"}, {`1`, "SCALAR"}, {`true`, "SCALAR"}, {`false`, "SCALAR"}, {`null`, "SCALAR"}} {
			fixture.SQLJSONBoth(t, p, b, c, fixture.Select(`SELECT $1::text IS JSON `+root.kind+` AS v`, []string{"v"}, []string{"BOOL"}, [][]any{{true}}, root.source))
		}
		held, err := c.PrepareContext(p.Ctx, `SELECT JSON($1::text WITH UNIQUE KEYS) AS v`)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		for _, tc := range []struct {
			input string
			bad   bool
		}{{`{"ok":1}`, false}, {`x`, true}, {`{"ok":2}`, false}} {
			rows, err := held.QueryContext(p.Ctx, tc.input)
			if tc.bad {
				if rows != nil {
					rows.Close()
					t.Fatal("malformed JSON returned cursor")
				}
				d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
				d.Detail = `Token "x" is invalid.`
				d.Context = `JSON data, line 1: x`
				fixture.AssertDiagnostic(t, err, &d)
			} else {
				if err != nil {
					t.Fatal(err)
				}
				fixture.StringCoreRetainedRows(t, rows, nil, []string{"v"}, []string{"JSON"}, [][]any{{tc.input}}, false)
			}
		}
		if err = held.Close(); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec(`INSERT INTO sqj_smoke VALUES(4,'x')`, 1))
		query, err := c.PrepareContext(p.Ctx, `SELECT JSON_ARRAY(SELECT doc FROM sqj_smoke WHERE id=$1::int FORMAT JSON) AS v`)
		if err != nil {
			t.Fatal(err)
		}
		defer query.Close()
		for _, tc := range []struct {
			id   int64
			want any
			bad  bool
		}{{1, `[{"n":1}]`, false}, {4, nil, true}, {0, nil, false}, {2, `[]`, false}, {3, `[{"n":3}]`, false}} {
			rows, err := query.QueryContext(p.Ctx, tc.id)
			if tc.bad {
				if rows != nil {
					rows.Close()
					t.Fatal("malformed query row returned cursor")
				}
				d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
				d.Detail = `Token "x" is invalid.`
				d.Context = `JSON data, line 1: x`
				fixture.AssertDiagnostic(t, err, &d)
			} else {
				if err != nil {
					t.Fatal(err)
				}
				fixture.StringCoreRetainedRows(t, rows, nil, []string{"v"}, []string{"JSON"}, [][]any{{tc.want}}, false)
			}
		}
		if err = query.Close(); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct{ expr, code, message string }{
			{`JSON_OBJECT('a':1,'a':NULL ABSENT ON NULL WITH UNIQUE KEYS)`, "22030", `duplicate JSON object key value: "a"`},
			{`JSON_OBJECT(NULL:1)`, "22004", "null value not allowed for object key"},
			{`JSON_OBJECT(NULL:NULL ABSENT ON NULL RETURNING jsonb)`, "22023", "argument 1: key must not be null"},
		} {
			fixture.SQLJSONDiagnostic(t, p, b, c, `SELECT `+tc.expr+` AS v`, fixture.ControlsReferenceDiagnostic(tc.code, tc.message), false)
		}
		run(fixture.Exec(`CREATE TABLE sqj_smoke_atomic(id int,j json)`, 0))
		run(fixture.Exec(`INSERT INTO sqj_smoke_atomic VALUES(1,JSON_ARRAY(1))`, 1))
		bad := fixture.Error(`INSERT INTO sqj_smoke_atomic VALUES(2,JSON('null')),(3,JSON('x'::text))`, "22P02")
		run(bad)
		run(fixture.Select(`SELECT id,j FROM sqj_smoke_atomic ORDER BY id`, []string{"id", "j"}, []string{"INT4", "JSON"}, [][]any{{int64(1), `[1]`}}))
		sqljsonControls(t, p, b, c)
	})
}
