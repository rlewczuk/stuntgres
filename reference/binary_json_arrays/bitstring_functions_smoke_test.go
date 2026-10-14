package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBitStringFunctionsFeatureSmoke(t *testing.T) {
	bitStringFunctionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		bitStringFunctionsMeasurements(t, p, b, c)
		bitStringFunctionsSlices(t, p, b, c)
		bitStringFunctionsBounds(t, p, b, c)
		bitStringFunctionsNullMetadata(t, p, b, c)
		bitStringFunctionsHeld(t, p, b, c)
		bitStringFunctionsControls(t, p, b, c)
	})
}
