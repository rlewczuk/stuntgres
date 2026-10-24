package fixture

import (
	"context"
	"database/sql"
	"testing"
)

// PLpgSQLIntegerForActiveCancel admits the actual integer-range walker before
// cancellation, including empty and CONTINUE-only bodies. The native walker
// keeps the executePLIntegerFor frame name; no synthetic progress signal is used.
// postgresContext is independently established on the pinned release oracle.
func PLpgSQLIntegerForActiveCancel(t *testing.T, p *Pair, b Backend, c *sql.Conn, query, postgresContext string, operation func(context.Context) RelationOutcome) {
	t.Helper()
	activeProviderCancel(t, p, b, c, query, "executePLIntegerFor", "", postgresContext, nil, operation)
}
