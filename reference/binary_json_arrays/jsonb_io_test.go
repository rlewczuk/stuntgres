package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Independently specified normalized outputs validated against pinned PG18.
func TestReferenceJSONBRepresentation(t *testing.T) {
	cases := []struct{ input, output string }{
		{` {"z":1,"a":1,"a":2} `, `{"a": 2, "z": 1}`},
		{`[1,true,null,{"b":2,"a":1}]`, `[1, true, null, {"a": 1, "b": 2}]`},
		{`null`, `null`}, {`"null"`, `"null"`}, {`true`, `true`}, {`false`, `false`},
		{`-0`, `0`}, {`-0.00`, `0.00`}, {`1.230e-5`, `0.00001230`}, {`1.230E+004`, `12300`},
		{`1.00`, `1.00`}, {`9223372036854775808`, `9223372036854775808`},
		{strings.Repeat("9", 400), strings.Repeat("9", 400)}, {`[]`, `[]`}, {`{}`, `{}`},
		{`{"a":1,"\u0061":2}`, `{"a": 2}`},
		{`{"é":1,"aaa":2,"雪":3,"z":4,"aa":5}`, `{"z": 4, "aa": 5, "é": 1, "aaa": 2, "雪": 3}`},
		{`[2,1,1,{"z":{"b":2,"a":1},"a":[]} ]`, `[2, 1, 1, {"a": [], "z": {"a": 1, "b": 2}}]`},
		{`"\"\\\/\b\f\n\r\t"`, `"\"\\/\b\f\n\r\t"`},
		{`"\uD800\uDC00"`, `"𐀀"`}, {`"\uD83D\uDE04"`, `"😄"`},
		{`"\u0061\u00e9\u96ea"`, `"aé雪"`}, {`"<>&\u2028"`, "\"<>&\u2028\""},
		{`"\\u0000\\uD800"`, `"\\u0000\\uD800"`},
		{`"\uDBFF\uDFFF"`, "\"\U0010ffff\""}, {`"\uFFFF"`, "\"\uffff\""},
		{`"\u0001\u001f\u007f"`, "\"\\u0001\\u001f\x7f\""},
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
				run(fixture.Exec("CREATE TABLE jb_io(id INT,v pg_catalog.jsonb)", 0))
				run(fixture.Exec("CREATE TABLE jb_literal(id INT,v JSONB)", 0))
				var want [][]any
				for i, tc := range cases {
					run(fixture.Exec("INSERT INTO jb_io VALUES($1,$2)", 1, i, tc.input))
					run(fixture.Exec("INSERT INTO jb_literal VALUES($1,"+jsonReferenceQuote(tc.input)+")", 1, i))
					want = append(want, []any{int64(i), tc.output})
					for _, expression := range []string{"jsonb " + jsonReferenceQuote(tc.input), "CAST(" + jsonReferenceQuote(tc.input) + " AS jsonb)", jsonReferenceQuote(tc.input) + "::pg_catalog.jsonb"} {
						run(fixture.Select("SELECT "+expression+" AS v", []string{"v"}, []string{"JSONB"}, [][]any{{tc.output}}))
					}
				}
				run(fixture.Exec("INSERT INTO jb_io(id) VALUES($1)", 1, len(cases)))
				run(fixture.Exec("INSERT INTO jb_literal(id) VALUES($1)", 1, len(cases)))
				want = append(want, []any{int64(len(cases)), nil})
				for _, table := range []string{"jb_io", "jb_literal"} {
					run(fixture.Select("SELECT id,v FROM "+table+" ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSONB"}, want))
				}
			}
		})
	}
}

func TestReferenceJSONBBoundFrames(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE jb_frames(id INT,v JSONB,n JSONB)", 0))
		q := "INSERT INTO jb_frames VALUES($1,$2,$2)"
		stmt, err := backend.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
		buf := []byte(` {"b":2,"b":1} `)
		var nilBytes []byte
		cases := []struct{ arg, want any }{{`{"a":1}`, `{"a": 1}`}, {buf, `{"b": 1}`}, {jsonReferenceValuer(`"\uD83D\uDE04"`), `"😄"`}, {nil, nil}, {nilBytes, nil}, {true, "true"}, {int64(42), "42"}, {1.25, "1.25"}, {"null", "null"}}
		var want [][]any
		for i, tc := range cases {
			fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(q, 1, i, tc.arg))
			want = append(want, []any{int64(i), tc.want, tc.want})
			if i == 1 {
				buf[0] = 'x'
			}
		}
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT id,v,n FROM jb_frames ORDER BY id", []string{"id", "v", "n"}, []string{"INT4", "JSONB", "JSONB"}, want))
	}
}

func TestReferenceJSONBNumericBoundaries(t *testing.T) {
	cases := []struct{ name, input, output string }{
		{"maximum_magnitude", "1e131071", "1" + strings.Repeat("0", 131071)},
		{"maximum_scale", "1e-16383", "0." + strings.Repeat("0", 16382) + "1"},
		{"mantissa_magnitude", "0.1e131072", "1" + strings.Repeat("0", 131071)},
		{"mantissa_scale", "10e-16383", "0." + strings.Repeat("0", 16381) + "10"},
		{"maximum_trailing_scale", "0." + strings.Repeat("0", 16383), "0." + strings.Repeat("0", 16383)},
		{"zero_positive_exponent", "0e131072", "0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				for _, prepared := range []bool{false, true} {
					for _, bound := range []bool{false, true} {
						q := "SELECT " + jsonReferenceQuote(tc.input) + "::jsonb AS v"
						var args []any
						if bound {
							q = "SELECT $1::jsonb AS v"
							args = []any{tc.input}
						}
						s := fixture.Select(q, []string{"v"}, []string{"JSONB"}, [][]any{{tc.output}}, args...)
						if prepared {
							s = fixture.Prepared(s)
						}
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					}
				}
			}
		})
	}
}
