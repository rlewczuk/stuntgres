package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBSetFeatureSmoke(t *testing.T) {
	jsonbSetScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ args, want string }{
			{`'{"a":1}','{a}','2'`, `{"a": 2}`},
			{`'{}','{a,b}','2'`, `{}`},
			{`'[1,2,3]','{-1}','9'`, `[1, 2, 9]`},
			{`'[1,2,3]','{3}','9'`, `[1, 2, 3, 9]`},
			{`'[1,2,3]','{3}','9',false`, `[1, 2, 3]`},
		} {
			s := fixture.JSONBSetMetadataStep(jsonbSetSelect("SELECT jsonb_set("+tc.args+") AS changed", tc.want), b.PG)
			jsonbSetBoth(t, p, b, c, s)
		}
		jsonbSetBoth(t, p, b, c, fixture.JSONBSetMetadataStep(jsonbSetSelect("SELECT jsonb_set(NULL,'{a}','2') AS changed", nil), b.PG))
		jsonbSetBoth(t, p, b, c, fixture.JSONBSetMetadataStep(fixture.Select("SELECT jsonb_set(v,'{a}','2') AS changed FROM (SELECT NULL::jsonb AS v) AS x WHERE false", []string{"changed"}, []string{"JSONB"}, nil), b.PG))
		jsonbSetHeldModes(t, p, b, c)
		jsonbSetBoth(t, p, b, c, jsonbSetSelect(`SELECT jsonb_set_lax('{"a":1}',path => '{a}',replacement => NULL,null_value_treatment => 'delete_key') AS changed`, `{}`))
		check := jsonbSetStored(t, p, b, c)
		jsonbSetAtomic(t, p, b, c)
		jsonbSetControls(t, p, b, c, check)
	})
}
