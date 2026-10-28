package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonpathArithmeticHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	q := `SELECT jsonb_path_query_array($1,$2,$3,$4) AS changed`
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, held, b.Recorder, s) }
	for _, tc := range []struct{ path, want string }{
		{`$.a + $.b`, `[3.500]`}, {`$.big + 1`, `[9007199254740994]`},
		{`$.neg / 2`, `[-3.5000000000000000]`}, {`1 / 3`, `[0.33333333333333333333]`},
	} {
		run(fixture.JSONBSetMetadataStep(jsonpathNavigateSelect(q, tc.want, jsonpathArithmeticDocument, tc.path, `{}`, false), b.PG))
	}
	run(jsonpathNavigateError(q, "22012", `division by zero`, `1`, `$ / 0`, `{}`, false))
	run(jsonpathNavigateSelect(q, `[-1.00]`, jsonpathArithmeticDocument, `$.neg % 2`, `{}`, false))
	run(jsonpathNavigateSelect(q, `[]`, `[]`, `$ + 1`, `{}`, true))
	run(jsonpathNavigateSelect(q, `[5.00]`, `2.00`, `$ + $n`, `{"n":3}`, false))
	run(jsonpathNavigateSelect(q, `[5]`, `[2]`, `strict $[0] + $n`, `{"n":3}`, false))
	for slot := range 4 {
		args := []any{jsonpathArithmeticDocument, `$.a + $.b`, `{}`, false}
		args[slot] = nil
		run(jsonpathNavigateSelect(q, nil, args...))
	}
	run(jsonpathNavigateError(q, "22038", `left operand of jsonpath operator + is not a single numeric value`, `null`, `$ + 1`, `{}`, false))
	// Caller-owned byte binds are detached from returned buffers. Consume the
	// first pq stream before reusing its physical connection; native additionally
	// retains an eager cursor across the following execution.
	doc, path := []byte(`1.20`), []byte(`$ + 2`)
	rows, err := stmt.QueryContext(p.Ctx, doc, path, []byte(`{}`), false)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	meta, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal("missing arithmetic row", rows.Err())
	}
	var owned []byte
	if err := rows.Scan(&owned); err != nil {
		t.Fatal(err)
	}
	doc[0], path[0] = '9', '1'
	if rows.Next() || rows.Err() != nil {
		t.Fatal("unexpected extra rows", rows.Err())
	}
	rows.Close()
	run(jsonpathNavigateSelect(q, `[7]`, `3`, `$ + 4`, `{}`, false))
	if string(owned) != `[3.20]` || len(meta) != 1 || meta[0].DatabaseTypeName() != "JSONB" {
		t.Fatalf("retained=%s metadata=%v", owned, meta)
	}
	if !b.PG {
		old, err := stmt.QueryContext(p.Ctx, `1.20`, `$ + 2`, `{}`, false)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		run(jsonpathNavigateSelect(q, `[9]`, `4`, `$ + 5`, `{}`, false))
		fixture.StringCoreRetainedRows(t, old, nil, []string{"changed"}, []string{"JSONB"}, [][]any{{`[3.20]`}}, false)
	}
}

func TestReferenceJSONPathArithmeticParameters(t *testing.T) {
	jsonpathNavigateScenario(t, jsonpathArithmeticHeld)
}

func TestReferenceJSONPathArithmeticMetadata(t *testing.T) {
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			doc, path string
			want      any
		}{
			{`1.20`, `$ + 2`, `[3.20]`}, {`{}`, `+$.missing`, `[]`},
			{`0`, `1 / $ > 0`, `[null]`}, {`null`, `$ + 1`, `[]`},
		} {
			for _, empty := range []bool{false, true} {
				q := jsonpathArithmeticSQL
				want := [][]any{{tc.want}}
				if empty {
					q += ` WHERE false`
					want = nil
				}
				fixture.JSONPathNavigateBoth(t, p, b, c, fixture.JSONBSetMetadataStep(fixture.Select(q, []string{"changed"}, []string{"JSONB"}, want, tc.doc, tc.path, `{}`, true), b.PG))
			}
		}
		fixture.JSONPathNavigateBoth(t, p, b, c, fixture.JSONBSetMetadataStep(jsonpathNavigateSelect(jsonpathArithmeticSQL, nil, nil, `$ + 1`, `{}`, false), b.PG))
	})
}
