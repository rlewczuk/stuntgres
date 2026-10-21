package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONPathNavigateRecursiveRanges(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			q := `SELECT jsonb_path_query_array($1::jsonb,$2::jsonpath) AS changed`
			doc := `{"a":1,"b":{"c":2},"d":[3,{"e":4}]}`
			for _, mode := range []string{"strict ", "lax "} {
				for _, tc := range []struct{ bounds, want string }{
					{"0 to 0", `[{"a": 1, "b": {"c": 2}, "d": [3, {"e": 4}]}]`},
					{"0 to 1", `[{"a": 1, "b": {"c": 2}, "d": [3, {"e": 4}]}, 1, {"c": 2}, [3, {"e": 4}]]`},
					{"0 to 2", `[{"a": 1, "b": {"c": 2}, "d": [3, {"e": 4}]}, 1, {"c": 2}, 2, [3, {"e": 4}], 3, {"e": 4}]`},
					{"0 to last", `[{"a": 1, "b": {"c": 2}, "d": [3, {"e": 4}]}, 1, {"c": 2}, 2, [3, {"e": 4}], 3, {"e": 4}, 4]`},
					{"1 to 0", `[]`},
					{"1 to 1", `[1, {"c": 2}, [3, {"e": 4}]]`},
					{"1 to 2", `[1, {"c": 2}, 2, [3, {"e": 4}], 3, {"e": 4}]`},
					{"1 to last", `[1, {"c": 2}, 2, [3, {"e": 4}], 3, {"e": 4}, 4]`},
					{"2 to 0", `[]`},
					{"2 to 1", `[]`},
					{"2 to 2", `[2, 3, {"e": 4}]`},
					{"2 to last", `[2, 3, {"e": 4}, 4]`},
					{"last to 0", `[]`},
					{"last to 1", `[]`},
					{"last to 2", `[]`},
					{"last to last", `[1, 2, 3, 4]`},
				} {
					t.Run(mode+tc.bounds, func(t *testing.T) {
						jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(q, tc.want, doc, mode+`$.**{`+tc.bounds+`}`))
					})
				}
				for _, path := range []string{`$.x.**{last to 1}`, `$.x.**{last to 1}[$v]`, `$.x.**{last to 1}.size()`, `$.x.**{last to 1}.double()`} {
					t.Run(mode+path, func(t *testing.T) {
						jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(q, `[]`, `{"x":{"a":1}}`, mode+path))
					})
				}
			}
		})
	}
}
