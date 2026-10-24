package fixture

import (
	"context"
	"database/sql"
	"testing"
)

// PLpgSQLLoopActiveCancel observes the procedural loop worker (including an
// empty body), cancels it, and joins before returning the physical owner.
// postgresContext is independently established on the pinned release oracle.
func PLpgSQLLoopActiveCancel(t *testing.T, p *Pair, b Backend, c *sql.Conn, query, postgresContext string, operation func(context.Context) RelationOutcome) {
	t.Helper()
	activeProviderCancel(t, p, b, c, query, "executePLLoop", "", postgresContext, nil, operation)
}
