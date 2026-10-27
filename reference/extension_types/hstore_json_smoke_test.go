package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceHstoreJSONFeatureSmoke(t *testing.T) {
	t.Run("conversion_metadata_and_atomicity", func(t *testing.T) {
		hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			hstoreJSONControlSeed(t, p, b, c)
			hstoreJSONControlVerify(t, p, b, c)
			q := hstoreJSONQuery("v") + " FROM hs_json_controls WHERE id=0"
			hstoreRun(t, p, b, c, hstoreJSONPair(b, q, [][]any{}))
			hstoreJSONAtomicity(t, p, b, c, "hstore_to_json_loose")
		})
	})
	t.Run("same_restore_and_actual_oracle_replay", func(t *testing.T) { hstoreJSONControlPhase(t, "same") })
	t.Run("fresh_authorized_restore", func(t *testing.T) { hstoreJSONControlPhase(t, "fresh") })
}
