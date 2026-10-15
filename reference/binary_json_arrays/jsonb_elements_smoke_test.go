package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBElementsFeatureSmoke(t *testing.T) {
	jsonTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, f := range []string{"jsonb_array_elements", "jsonb_array_elements_text"} {
			jsonTypeBoth(t, p, b, c, jsonbElementsMetadata(jsonbElementsOrdered(f, jsonbElementsMixed, jsonbElementsMixedRows(f)), b.PG))
			jsonTypeBoth(t, p, b, c, jsonbElementsOrdered(f, `[[],{}]`, [][]any{{"[]", int64(1)}, {"{}", int64(2)}}))
			for _, suffix := range []string{" AS j", " AS j(v)"} {
				col := "value"
				if suffix == " AS j(v)" {
					col = "v"
				}
				q := "SELECT * FROM " + f + "($1)" + suffix
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for _, tc := range []struct {
					input   any
					rows    [][]any
					message string
				}{
					{`[1]`, [][]any{{"1"}}, ""}, {`[]`, nil, ""}, {nil, nil, ""}, {`[null]`, [][]any{{"null"}}, ""}, {`{}`, nil, "cannot extract elements from an object"}, {`false`, nil, "cannot extract elements from a scalar"}, {`[2]`, [][]any{{"2"}}, ""},
				} {
					if tc.input == `[null]` && jsonbElementsType(f) == "TEXT" {
						tc.rows = [][]any{{nil}}
					}
					s := jsonbElementsMetadata(jsonbElementsSelect(f, q, []string{col}, tc.rows, tc.input), b.PG)
					if tc.message != "" {
						s = jsonTypeError(q, "22023", tc.message, 0, tc.input)
					}
					jsonTypeRun(t, p, b, c, s)
					jsonTypeRun(t, p, b, held, s)
				}
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
			}
			jsonTypeBoth(t, p, b, c, jsonbElementsMetadata(jsonbElementsSelect(f, "SELECT "+f+"('[1]')", []string{f}, [][]any{{"1"}}), b.PG))
			jsonTypeBoth(t, p, b, c, jsonTypeError("SELECT "+f+"('{}'::jsonb)", "22023", "cannot extract elements from an object", 0))
		}
		jsonbElementsAtomic(t, p, b, c)
	})
}
