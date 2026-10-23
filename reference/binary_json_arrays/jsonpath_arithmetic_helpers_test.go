package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

const jsonpathArithmeticDocument = `{"a":1.20,"b":2.300,"neg":-7.00,"seq":[2,3,4],"one":[2],"nested":[[2]],"big":9007199254740993}`
const jsonpathArithmeticSQL = `SELECT jsonb_path_query_array($1::jsonb,$2::jsonpath,$3::jsonb,$4::boolean) AS changed`

func jsonpathArithmeticCases(t *testing.T, cases []jsonpathNavigateCase) {
	t.Helper()
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range cases {
			t.Run(tc.path, func(t *testing.T) {
				vars := tc.vars
				if vars == "" {
					vars = "{}"
				}
				jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(jsonpathArithmeticSQL, tc.want, tc.doc, tc.path, vars, false))
			})
		}
	})
}
