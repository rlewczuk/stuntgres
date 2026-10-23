package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONPathArithmeticFeatureSmoke(t *testing.T) {
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(jsonpathArithmeticSQL, `[3.500]`, jsonpathArithmeticDocument, `$.a + $.b`, `{}`, false))
		jsonpathArithmeticHeld(t, p, b, c)
		jsonpathArithmeticStored(t, p, b, c)
	})
}
