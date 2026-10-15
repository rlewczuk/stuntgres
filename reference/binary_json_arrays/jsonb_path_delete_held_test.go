package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonbPathDeleteHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	q := "SELECT $1 #- $2 AS v"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, held, b.Recorder, s) }
	good := jsonbPathDeleteMetadata(jsonbPathDeleteSelect(q, `{"b": 2}`, `{"a":1,"b":2}`, "{a}"), b.PG)
	run(good)
	jsonTypeRun(t, p, b, c, good)
	run(jsonbPathDeleteSelect(q, `[1]`, `[1,2]`, "{-1}"))
	run(jsonbPathDeleteSelect(q, `{"a": 1}`, `{"a":1}`, "{missing}"))
	for _, tc := range []struct{ doc, path, code, msg, detail, context string }{
		{`[1]`, "{x}", "22P02", `path element at position 1 is not an integer: "x"`, "", ""},
		{`{"a":1}`, "{a,NULL}", "22004", "path element at position 2 is null", "", ""},
		{`{}`, "{{a,b}}", "2202E", "wrong number of array subscripts", "", ""},
		{`null`, "{}", "22023", "cannot delete path in scalar", "", ""},
		{`{}`, "{a,,b}", "22P02", `malformed array literal: "{a,,b}"`, `Unexpected "," character.`, "unnamed portal parameter $2 = '...'"},
		{`TRUE`, "{a}", "22P02", "invalid input syntax for type json", `Token "TRUE" is invalid.`, "JSON data, line 1: TRUE\nunnamed portal parameter $1 = '...'"},
	} {
		d := fixture.ControlsReferenceDiagnostic(tc.code, tc.msg)
		d.Detail, d.Context = tc.detail, tc.context
		s := jsonbPathDeleteError(q, d, tc.doc, tc.path)
		run(s)
		jsonTypeRun(t, p, b, c, s)
		run(good)
	}
	for _, args := range [][]any{{nil, "{a}"}, {`{}`, nil}, {nil, nil}} {
		run(jsonbPathDeleteSelect(q, nil, args...))
		run(good)
	}
	doc, path := []byte(`{"a":1,"b":2}`), []byte("{a}")
	rows, err := stmt.QueryContext(p.Ctx, doc, path)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("missing byte-input row", rows.Err())
	}
	var before string
	if err := rows.Scan(&before); err != nil {
		t.Fatal(err)
	}
	for i := range doc {
		doc[i] = 'x'
	}
	for i := range path {
		path[i] = 'x'
	}
	var after string
	if err := rows.Scan(&after); err != nil || before != `{"b": 2}` || after != before {
		t.Fatalf("retained=%q/%q err=%v", before, after, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("extra byte-input row", rows.Err())
	}
	rows.Close()
	run(good)
	jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE jbd_owned(doc jsonb)", 0))
	jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO jbd_owned VALUES('{"a":1,"b":2}')`, 1))
	jsonTypeBoth(t, p, b, c, fixture.Select("SELECT doc,doc #- '{a}' AS a,doc #- '{b}' AS b FROM jbd_owned", []string{"doc", "a", "b"}, []string{"JSONB", "JSONB", "JSONB"}, [][]any{{`{"a": 1, "b": 2}`, `{"b": 2}`, `{"a": 1}`}}))
}

func TestReferenceJSONBPathDeleteHeld(t *testing.T) {
	jsonbPathDeleteScenario(t, jsonbPathDeleteHeld)
}
