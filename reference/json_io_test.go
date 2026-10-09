package reference_test

import (
	"database/sql/driver"
	"strings"
	"testing"
)

type jsonReferenceValuer string

func (v jsonReferenceValuer) Value() (driver.Value, error) { return string(v), nil }

func jsonReferenceQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func TestReferenceJSONRepresentation(t *testing.T) {
	documents := []string{
		" \t\n{\"z\":1.230e-5,\"a\":1,\"a\":2}\r\n ",
		`null`, `"null"`, `true`, `false`, `-0`, `1.230E+004`,
		`9223372036854775808`, `1e100000000000000000000`,
		strings.Repeat("9", 400), `[]`, `{}`, `[{},[],true,false,null,{"a":[1,2]}]`,
		`"\"\\\/\b\f\n\r\t"`, `"\u0000"`, `"\uD800"`, `"\uDC00"`,
		`"\uD800\uD800"`, `"\uD800\u0041"`, `"\uD800\uDC00"`, `"\uaBcD"`,
		`{"é":"雪","apostrophe":"it's"}`,
	}
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				run := func(s referenceStep) {
					if prepared {
						s = referencePrepared(s)
					}
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
				}
				run(referenceExec("CREATE TABLE json_io(id INTEGER,v JSON)", 0))
				run(referenceExec("CREATE TABLE json_literals(id INTEGER,v JSON)", 0))
				want := make([][]any, 0, len(documents)+1)
				for i, doc := range documents {
					run(referenceExec("INSERT INTO json_io VALUES($1,$2)", 1, i, doc))
					run(referenceExec("INSERT INTO json_literals VALUES($1,"+jsonReferenceQuote(doc)+")", 1, i))
					want = append(want, []any{int64(i), doc})
				}
				run(referenceExec("INSERT INTO json_io(id) VALUES($1)", 1, len(documents)))
				run(referenceExec("INSERT INTO json_literals(id) VALUES($1)", 1, len(documents)))
				want = append(want, []any{int64(len(documents)), nil})
				run(referenceSelect("SELECT id,v FROM json_io ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSON"}, want))
				run(referenceSelect("SELECT id,v FROM json_literals ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSON"}, want))
			}
		})
	}
}

func TestReferenceJSONBoundFrames(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("CREATE TABLE json_frames(id INTEGER,v JSON,n JSON)", 0))
		q := "INSERT INTO json_frames VALUES($1,$2,$2)"
		stmt, err := backend.db.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}
		buf := []byte(` {"b":2,"b":1} `)
		var nilBytes []byte
		cases := []struct {
			arg  any
			want any
		}{
			{`{"a":1}`, `{"a":1}`}, {buf, ` {"b":2,"b":1} `},
			{jsonReferenceValuer(`"\uD800"`), `"\uD800"`},
			{nil, nil}, {nilBytes, nil}, {true, "true"}, {int64(42), "42"}, {1.25, "1.25"},
			{"null", "null"},
		}
		want := make([][]any, 0, len(cases))
		for i, tc := range cases {
			referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(q, 1, i, tc.arg))
			want = append(want, []any{int64(i), tc.want, tc.want})
			if i == 1 {
				buf[0] = 'x'
			}
		}
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT id,v,n FROM json_frames ORDER BY id", []string{"id", "v", "n"}, []string{"INT4", "JSON", "JSON"}, want))
	}
}
