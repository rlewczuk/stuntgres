package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONKeysReviewDiagnostics(t *testing.T) {
	jsonKeysScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, prefix := range []string{"SELECT ", "/*é*/ SELECT "} {
			for _, tc := range []struct{ call, message string }{
				{`pg_catalog.json_object_keys('{}') FILTER (WHERE true)`, "FILTER specified, but pg_catalog.json_object_keys is not an aggregate function"},
				{`pg_catalog.json_object_keys(DISTINCT '{}')`, "DISTINCT specified, but pg_catalog.json_object_keys is not an aggregate function"},
				{`pg_catalog.json_object_keys('{}' ORDER BY 1)`, "ORDER BY specified, but pg_catalog.json_object_keys is not an aggregate function"},
				{`pg_catalog.json_object_keys('{}') OVER ()`, "OVER specified, but pg_catalog.json_object_keys is not a window function nor an aggregate function"},
			} {
				q := prefix + tc.call
				d := fixture.ControlsReferenceDiagnostic("42809", tc.message)
				d.Position = fixture.Position(len([]rune(prefix)) + 1)
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s prepared=%t", q, prepared), func(t *testing.T) {
						stage := "execute"
						if prepared {
							stage = "prepare"
						}
						fixture.JSONKeysError(t, p, b, c, q, prepared, d, stage)
					})
				}
			}
			for _, tc := range []struct{ call, code, message, token string }{
				{`json_object_keys('{}') FILTER (WHERE 1)`, "42804", "argument of FILTER must be type boolean, not type integer", "1"},
				{`json_object_keys('{}') FILTER (WHERE missing)`, "42703", `column "missing" does not exist`, "missing"},
				{`pg_catalog.json_object_keys('{}') FILTER (WHERE 1)`, "42804", "argument of FILTER must be type boolean, not type integer", "1"},
				{`json_object_keys(1) FILTER (WHERE 1)`, "42804", "argument of FILTER must be type boolean, not type integer", "1"},
				{`json_object_keys('{}'::text) FILTER (WHERE missing)`, "42703", `column "missing" does not exist`, "missing"},
				{`json_object_keys('{}','{}') FILTER (WHERE 1)`, "42804", "argument of FILTER must be type boolean, not type integer", "1"},
			} {
				q := prefix + tc.call
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Position = fixture.Position(len([]rune(q[:strings.LastIndex(q, tc.token)])) + 1)
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s prepared=%t", q, prepared), func(t *testing.T) {
						stage := "execute"
						if prepared {
							stage = "prepare"
						}
						fixture.JSONKeysError(t, p, b, c, q, prepared, d, stage)
					})
				}
			}
		}
	})
}
