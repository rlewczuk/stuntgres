package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// The smoke asserts actual new moments, held typed parameters, metadata,
// native symbolic view restoration and failed INSERT publication.
func TestReferenceStatisticalAggregateSmoke(t *testing.T) {
	statisticalScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, r *fixture.Recorder, pg bool) {
		statisticalPersistent(t, p, conn, r, pg)
		statisticalAtomicity(t, p, conn, r)
	})
}
