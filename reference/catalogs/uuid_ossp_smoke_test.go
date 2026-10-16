package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUUIDOsspFeatureSmoke(t *testing.T) {
	uuidOsspCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		uuidOsspControls(t, p, c, r, pg)
		owner := "postgres"
		if pg {
			owner = "stuntgres"
		}
		uuidOsspInventory(t, p, c, owner, owner)
		uuidOsspGeneration(t, p, c, r, pg, "uuid_ossp_part.uuid_generate_v4()")
		q := "SELECT uuid_ossp_part.uuid_generate_v4(NULL)"
		uuidOsspFailure(t, p, c, q, uuidOsspMissing("uuid_ossp_part.uuid_generate_v4(unknown)", 8))
	})
}
