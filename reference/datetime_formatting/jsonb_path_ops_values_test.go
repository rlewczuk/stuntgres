package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBPathOpsValues(t *testing.T) {
	jsonbPathOpsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			doc, path     string
			exists, match any
		}{
			{`false`, `$`, true, false}, {`true`, `$`, true, true},
			{`null`, `$`, true, nil}, {`1`, `$`, true, nil},
			{`[true,false]`, `$[*]`, true, nil}, {`[]`, `$[*]`, false, nil},
			{`{"a":1}`, `$.missing`, false, nil}, {`{"a":1}`, `strict $.missing`, nil, nil},
			{`1`, `$ == 2`, true, false}, {`"x"`, `$ > 1`, true, nil},
			{`{}`, `exists($.missing)`, true, false}, {`{}`, `strict exists($.missing)`, true, nil},
		} {
			for i, op := range jsonbPathOps {
				t.Run(op+"/"+tc.doc+"/"+tc.path, func(t *testing.T) {
					want := tc.exists
					if i == 1 {
						want = tc.match
					}
					fixture.JSONPathNavigateBoth(t, p, b, c, jsonbPathOpsOne(jsonbPathOpsSQL(op), want, tc.doc, tc.path))
				})
			}
		}
	})
}

func TestReferenceJSONBPathOpsNulls(t *testing.T) {
	jsonbPathOpsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, op := range jsonbPathOps {
			for _, args := range [][]any{{nil, `$`}, {`true`, nil}, {nil, nil}} {
				fixture.JSONPathNavigateBoth(t, p, b, c, jsonbPathOpsMetadata(jsonbPathOpsOne(jsonbPathOpsSQL(op), nil, args...)))
			}
		}
	})
}

func TestReferenceJSONBPathOpsDemand(t *testing.T) {
	jsonbPathOpsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			doc   string
			match any
		}{
			{`[{"x":true},{}]`, true}, {`[{"x":false},{}]`, false}, {`[{"x":true},{"x":false},{}]`, nil},
		} {
			for i, op := range jsonbPathOps {
				var want any
				if i == 1 {
					want = tc.match
				}
				fixture.JSONPathNavigateBoth(t, p, b, c, jsonbPathOpsOne(jsonbPathOpsSQL(op), want, tc.doc, `strict $[*].x`))
			}
		}
		for _, op := range jsonbPathOps {
			q := jsonbPathOpsSQL(op)
			for _, path := range []string{`$[0,$v]`, `strict $[0,$v]`} {
				t.Run(op+"/"+path, func(t *testing.T) {
					if op == "@?" && path == `$[0,$v]` {
						fixture.JSONPathNavigateBoth(t, p, b, c, jsonbPathOpsOne(q, true, `[true]`, path))
					} else {
						fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateError(q, "42704", `could not find jsonpath variable "v"`, `[true]`, path))
					}
				})
			}
		}
	})
}
