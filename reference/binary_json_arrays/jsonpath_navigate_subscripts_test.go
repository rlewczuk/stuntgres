package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONPathNavigateSubscripts(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			q := `SELECT jsonb_path_query_array($1::jsonb,$2::jsonpath,'{}'::jsonb,$3::boolean) AS changed`
			for _, silent := range []bool{false, true} {
				name := "nonsilent"
				if silent {
					name = "silent"
				}
				t.Run(name, func(t *testing.T) {
					// Two subscript values already establish invalid cardinality, but
					// the third candidate must still demand the ordinary variable error.
					t.Run("late_missing_variable", func(t *testing.T) {
						jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateError(q, "42704", `could not find jsonpath variable "v"`, `[0,0,1]`, `$[$[*]?(@ == 0 || $v == 1)]`, silent))
					})

					// Likewise, a late structural failure precedes cardinality
					// validation. Silent collection has no outer item prefix yet.
					t.Run("late_structural_failure", func(t *testing.T) {
						structural := jsonpathNavigateError(q, "2203A", `JSON object does not contain key "i"`, `[{"i":0},{"i":1},{}]`, `strict $[$[*].i]`, silent)
						if silent {
							structural = jsonpathNavigateSelect(q, `[]`, `[{"i":0},{"i":1},{}]`, `strict $[$[*].i]`, silent)
						}
						jsonpathNavigateBoth(t, p, b, c, structural)
					})

					// Recursive continuation ignores structural bounds errors and
					// clips surviving elements rather than discarding an entire range.
					for _, tc := range []struct{ path, want string }{
						{`strict $.**{0}[-1 to 0]`, `[10]`},
						{`strict $.**{0}[1 to 5]`, `[20]`},
						{`strict $.**{0}[-3 to -1]`, `[]`},
						{`strict $.**{0}[2 to 5]`, `[]`},
						{`strict $.**{0}[1 to 0]`, `[]`},
					} {
						t.Run(tc.path, func(t *testing.T) {
							jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(q, tc.want, `[10,20]`, tc.path, silent))
						})
					}

					// The same invalid bounds remain errors without recursive
					// structural-ignore scope, or empty arrays when explicitly silent.
					for _, path := range []string{`strict $[-1 to 0]`, `strict $[1 to 5]`, `strict $[1 to 0]`} {
						t.Run(path, func(t *testing.T) {
							s := jsonpathNavigateError(q, "22033", `jsonpath array subscript is out of bounds`, `[10,20]`, path, silent)
							if silent {
								s = jsonpathNavigateSelect(q, `[]`, `[10,20]`, path, silent)
							}
							jsonpathNavigateBoth(t, p, b, c, s)
						})
					}
				})
			}
		})
	}
}
