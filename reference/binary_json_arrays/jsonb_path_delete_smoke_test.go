package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONBPathDeleteFeatureSmoke(t *testing.T) {
	jsonbPathDeleteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			path string
			want any
		}{
			{"{a,-1,x}", `{"a": [{"x": 1}, {}], "keep": 3}`},
			{"{a,-1}", `{"a": [{"x": 1}], "keep": 3}`},
			{"{missing}", `{"a": [{"x": 1}, {"x": 2}], "keep": 3}`},
		} {
			s := jsonbPathDeleteSelect("SELECT $1::jsonb #- $2::text[] AS v", tc.want, `{"a":[{"x":1},{"x":2}],"keep":3}`, tc.path)
			jsonTypeBoth(t, p, b, c, jsonbPathDeleteMetadata(s, b.PG))
		}
		jsonTypeBoth(t, p, b, c, jsonbPathDeleteMetadata(jsonbPathDeleteSelect("SELECT NULL::jsonb #- '{a}' AS v", nil), b.PG))
		jsonbPathDeleteHeld(t, p, b, c)
		check := jsonbPathDeleteStored(t, p, b, c)
		jsonbPathDeleteControls(t, p, b, c, check)
		if b.PG {
			jsonbPathDeleteFresh(t, p, c, b.Recorder)
		}
	})
}
