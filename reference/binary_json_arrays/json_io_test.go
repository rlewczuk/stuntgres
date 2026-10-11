package reference_test

import (
	"database/sql/driver"
	"strings"
	"stuntgres/reference/fixture"
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
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				run := func(s fixture.Step) {
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				}
				run(fixture.Exec("CREATE TABLE json_io(id INTEGER,v JSON)", 0))
				run(fixture.Exec("CREATE TABLE json_literals(id INTEGER,v JSON)", 0))
				want := make([][]any, 0, len(documents)+1)
				for i, doc := range documents {
					run(fixture.Exec("INSERT INTO json_io VALUES($1,$2)", 1, i, doc))
					run(fixture.Exec("INSERT INTO json_literals VALUES($1,"+jsonReferenceQuote(doc)+")", 1, i))
					want = append(want, []any{int64(i), doc})
				}
				run(fixture.Exec("INSERT INTO json_io(id) VALUES($1)", 1, len(documents)))
				run(fixture.Exec("INSERT INTO json_literals(id) VALUES($1)", 1, len(documents)))
				want = append(want, []any{int64(len(documents)), nil})
				run(fixture.Select("SELECT id,v FROM json_io ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSON"}, want))
				run(fixture.Select("SELECT id,v FROM json_literals ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSON"}, want))
			}
		})
	}
}

func TestReferenceJSONBoundFrames(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE json_frames(id INTEGER,v JSON,n JSON)", 0))
		q := "INSERT INTO json_frames VALUES($1,$2,$2)"
		stmt, err := backend.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
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
			fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(q, 1, i, tc.arg))
			want = append(want, []any{int64(i), tc.want, tc.want})
			if i == 1 {
				buf[0] = 'x'
			}
		}
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT id,v,n FROM json_frames ORDER BY id", []string{"id", "v", "n"}, []string{"INT4", "JSON", "JSON"}, want))
	}
}
