package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONBInsertFeatureSmoke(t *testing.T) {
	jsonbInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, jsonbSetSelect(`SELECT jsonb_insert('{"a":[0,1,2]}','{a,1}','"x"') AS changed`, `{"a": [0, "x", 1, 2]}`))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(jsonbSetSelect(`SELECT jsonb_insert('{"a":[0,1,2]}','{a,1}','"x"',true) AS changed`, `{"a": [0, 1, "x", 2]}`)))
		jsonbInsertHeld(t, p, b, c)
		jsonbInsertAtomic(t, p, b, c)
		check := jsonbInsertStored(t, p, b, c)
		jsonbInsertControls(t, p, b, c, check)
		jsonbInsertFuture(t, p, b, c)
	})
}
