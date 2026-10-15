package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONBConcatDeleteFeatureSmoke(t *testing.T) {
	jsonbConcatDeleteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			q    string
			want any
		}{
			{`SELECT '{"a":{"x":1},"b":2}'::jsonb || '{"a":{"y":3}}' AS v`, `{"a": {"y": 3}, "b": 2}`},
			{`SELECT '3'::jsonb || '[1,2]' AS v`, `[3, 1, 2]`},
			{`SELECT '["x",1,"x",2]'::jsonb - 'x' AS v`, `[1, 2]`},
			{`SELECT '[1,2,3]'::jsonb - -1 AS v`, `[1, 2]`},
			{`SELECT NULL::jsonb - 0 AS v`, nil},
		} {
			jsonTypeBoth(t, p, b, c, jsonbPathDeleteMetadata(jsonbConcatDeleteSelect(tc.q, tc.want), b.PG))
		}
		jsonbConcatDeleteHeld(t, p, b, c)
		check := jsonbConcatDeleteStored(t, p, b, c)
		jsonbConcatDeleteControls(t, p, b, c, check)
	})
}
