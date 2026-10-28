package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonElementsValues(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	const input = ` [ 1.00e+02, "x\n", null, "null", true, { "a":1,"a":2 }, [2,  false], 1e999999 ] `
	for _, f := range []string{"json_array_elements", "json_array_elements_text"} {
		values := []any{"1.00e+02", `"x\n"`, "null", `"null"`, "true", `{ "a":1,"a":2 }`, `[2,  false]`, "1e999999"}
		if f == "json_array_elements_text" {
			values[1], values[2], values[3] = "x\n", nil, "null"
		}
		var want [][]any
		for i, value := range values {
			want = append(want, []any{value, int64(i + 1)})
		}
		s := fixture.Select("SELECT value,n FROM "+f+"($1) WITH ORDINALITY AS j(value,n) ORDER BY n", []string{"value", "n"}, []string{jsonElementsType(f), "INT8"}, want, input)
		s.CheckScan = func(t *testing.T, rows *sql.Rows, i int) {
			var value sql.NullString
			var ordinal int64
			if err := rows.Scan(&value, &ordinal); err != nil {
				t.Fatal(err)
			}
			if value.Valid != (values[i] != nil) || (value.Valid && value.String != values[i]) || ordinal != int64(i+1) {
				t.Fatalf("scan row %d = %#v/%d", i, value, ordinal)
			}
			var raw any
			if err := rows.Scan(&raw, &ordinal); err != nil {
				t.Fatal(err)
			}
			if values[i] == nil {
				if raw != nil {
					t.Fatalf("NULL carrier=%T", raw)
				}
			} else if b.PG && f == "json_array_elements" {
				if _, ok := raw.([]byte); !ok {
					t.Fatalf("JSON carrier=%T", raw)
				}
			} else if _, ok := raw.(string); !ok {
				t.Fatalf("scalar carrier=%T", raw)
			}
		}
		fixture.JSONKeysBoth(t, p, b, c, jsonElementsMetadata(s, b.PG))
		for _, tc := range []struct {
			input     string
			raw, text [][]any
		}{
			{`[[],{},1,1]`, [][]any{{"[]"}, {"{}"}, {"1"}, {"1"}}, [][]any{{"[]"}, {"{}"}, {"1"}, {"1"}}},
			{`["\ud834\udd1e","a\"b\\c"]`, [][]any{{`"\ud834\udd1e"`}, {`"a\"b\\c"`}}, [][]any{{"𝄞"}, {"a\"b\\c"}}},
		} {
			want := tc.raw
			if f == "json_array_elements_text" {
				want = tc.text
			}
			s := jsonElementsSelect(f, "SELECT * FROM "+f+"($1)", want, tc.input)
			s.Bag = true
			fixture.JSONKeysBoth(t, p, b, c, s)
		}
	}
}

func TestReferenceJSONElementsValues(t *testing.T) {
	jsonElementsScenario(t, jsonElementsValues)
}

func jsonElementsNullMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, f := range []string{"json_array_elements", "json_array_elements_text"} {
		for _, input := range []any{`[1]`, `[]`, nil, `[null]`, `["null"]`} {
			for _, tc := range []struct {
				query, label string
				ordinal      bool
			}{
				{"SELECT " + f + "($1)", f, false},
				{"SELECT " + f + "($1) AS result", "result", false},
				{"SELECT * FROM " + f + "($1)", "value", false},
				{"SELECT j.value FROM " + f + "($1) AS j", "value", false},
				{"SELECT j.* FROM " + f + "($1) AS j", "value", false},
				{"SELECT * FROM " + f + "($1) AS j(v)", "v", false},
				{"SELECT * FROM " + f + "($1) WITH ORDINALITY", "value", true},
			} {
				var want [][]any
				if input != nil && input != `[]` {
					value := any("1")
					if input == `[null]` {
						value = "null"
						if f == "json_array_elements_text" {
							value = nil
						}
					}
					if input == `["null"]` {
						value = `"null"`
						if f == "json_array_elements_text" {
							value = "null"
						}
					}
					want = [][]any{{value}}
					if tc.ordinal {
						want[0] = append(want[0], int64(1))
					}
				}
				names, types := []string{tc.label}, []string{jsonElementsType(f)}
				if tc.ordinal {
					names = append(names, "ordinality")
					types = append(types, "INT8")
				}
				fixture.JSONKeysBoth(t, p, b, c, jsonElementsMetadata(fixture.Select(tc.query, names, types, want, input), b.PG))
			}
		}
	}
}

func TestReferenceJSONElementsNullMetadata(t *testing.T) {
	jsonElementsScenario(t, jsonElementsNullMetadata)
}
