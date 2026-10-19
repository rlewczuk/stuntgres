package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBPathOpsSuppression(t *testing.T) {
	jsonbPathOpsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ doc, path string }{
			{`{}`, `strict $.missing`}, {`1`, `strict $.x`}, {`1`, `strict $.*`},
			{`1`, `strict $[0]`}, {`1`, `strict $[*]`}, {`[1]`, `strict $[1]`},
			{`[1]`, `$[10000000000000000]`}, {`[1]`, `$["x"]`}, {`[1,2]`, `$[$[*]]`},
		} {
			for _, op := range jsonbPathOps {
				t.Run(op+"/"+tc.path, func(t *testing.T) {
					jsonpathNavigateBoth(t, p, b, c, jsonbPathOpsOne(jsonbPathOpsSQL(op), nil, tc.doc, tc.path))
				})
			}
		}
	})
}

func TestReferenceJSONBPathOpsCapabilityBoundary(t *testing.T) {
	jsonbPathOpsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			doc, path, message string
			exists, match      any
		}{
			{`1`, `$ / 0`, `stuntgres: JSONPath arithmetic is unsupported`, nil, nil},
			{`"not-a-date"`, `$.datetime()`, `stuntgres: JSONPath scalar methods are unsupported`, nil, nil},
			{`[1,2]`, `$.size()`, `stuntgres: JSONPath scalar methods are unsupported`, true, nil},
		} {
			for i, op := range jsonbPathOps {
				t.Run(op+"/"+tc.path, func(t *testing.T) {
					want := tc.exists
					if i == 1 {
						want = tc.match
					}
					s := jsonbPathOpsOne(jsonbPathOpsSQL(op), want, tc.doc, tc.path)
					if !b.PG {
						s = jsonpathNavigateError(s.Query, "0A000", tc.message, tc.doc, tc.path)
					}
					jsonpathNavigateBoth(t, p, b, c, s)
				})
			}
		}
		for i, op := range jsonbPathOps {
			var empty any
			if i == 0 {
				empty = false
			}
			jsonpathNavigateBoth(t, p, b, c, jsonbPathOpsOne(jsonbPathOpsSQL(op), empty, `{}`, `$.missing.datetime()`))
			jsonpathNavigateBoth(t, p, b, c, jsonbPathOpsOne(jsonbPathOpsSQL(op), true, `1`, `$ == 1 || $ / 0 == 0`))
			want := true
			if i == 1 {
				want = false
			}
			jsonpathNavigateBoth(t, p, b, c, jsonbPathOpsOne(jsonbPathOpsSQL(op), want, `1`, `$ == 2 && $ / 0 == 0`))
		}
	})
}
