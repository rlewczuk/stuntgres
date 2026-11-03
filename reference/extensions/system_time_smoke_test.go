package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSystemTimeFeatureSmoke(t *testing.T) {
	stCase(t, func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		stDeclaration(t,p,b,c)
		stValues(t,p,b,c)
		stDiagnostics(t,p,b,c)
		stElapsed(t,p,b,c)
		stCancellation(t,p,b,c)
		stControls(t,p,b,c)
		// Keep the RLS role/ACL setup after replay, so the independent native
		// destination's feature proof does not depend on an unrelated cluster role.
		stScanCancellation(t,p,b,c)
	})
}
