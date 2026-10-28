package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONPathNavigateRecursiveBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			q := `SELECT jsonb_path_query_array($1::jsonb,$2::jsonpath) AS changed`
			for _, mode := range []string{"strict ", "lax "} {
				for _, tc := range []struct{ doc, selected string }{
					{`1`, `[1]`}, {`null`, `[null]`}, {`"s"`, `["s"]`}, {`true`, `[true]`},
				} {
					t.Run(mode+tc.doc, func(t *testing.T) {
						// A scalar starting item is not a descendant leaf. Explicit
						// depth-zero bounds, however, still select that starting item.
						for _, path := range []string{`$.**{last}`, `$.a.**{last}`} {
							doc := tc.doc
							if path == `$.a.**{last}` {
								doc = `{"a":` + tc.doc + `}`
							}
							fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(q, `[]`, doc, mode+path))
						}
						for _, bounds := range []string{`{0}`, `{0 to last}`} {
							fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(q, tc.selected, tc.doc, mode+`$.**`+bounds))
						}
					})
				}
				fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(q, `[1, 2, null, "s", true]`, `{"a":1,"b":[2,null,"s",true]}`, mode+`$.**{last}`))
				for _, path := range []string{`$.**{last}.size()`, `$.**{last}.double()`, `$.**{last}[$v]`, `$.a.**{last}[$v]`} {
					t.Run(mode+path, func(t *testing.T) {
						doc := `1`
						if path == `$.a.**{last}[$v]` {
							doc = `{"a":1}`
						}
						// Empty recursion must not demand a missing variable or the
						// deliberately unsupported native scalar-method continuation.
						fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(q, `[]`, doc, mode+path))
					})
				}
			}
		})
	}
}
