package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// The smoke exercises new values, metadata, runtime errors, held recovery,
// correlation and symbolic stored consumers, not a baseline unsupported call.
func TestReferenceJSONElementsFeatureSmoke(t *testing.T) {
	jsonElementsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonElementsValues(t, p, b, c)
		jsonElementsNullMetadata(t, p, b, c)
		jsonElementsRootErrors(t, p, b, c)
		jsonElementsHeld(t, p, b, c)
		jsonElementsQuery(t, p, b, c)
		checks := jsonElementsStored(t, p, b, c)
		jsonElementsControls(t, p, b, c, checks)
	})
}
