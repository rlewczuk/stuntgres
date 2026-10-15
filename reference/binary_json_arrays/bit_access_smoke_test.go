package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBitAccessFeatureSmoke(t *testing.T) {
	bitAccessScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		bitAccessValues(t, p, b, c)
		bitAccessNullMetadata(t, p, b, c)
		bitAccessDiagnostics(t, p, b, c)
		bitAccessHeld(t, p, b, c)
		bitAccessOwnership(t, p, b, c)
		bitAccessControls(t, p, b, c)
	})
}
