package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// Register before the first committed stock mutation. Unlike fixture public
// cleanup, this restores cluster-global plpgsql membership even after Fatal.
func ownershipStockCleanup(t *testing.T, c *sql.Conn) func() {
	t.Helper()
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION"} {
			c.ExecContext(ctx, q)
		}
		for _, target := range []struct {
			sql, id string
			class   int
		}{
			{"LANGUAGE plpgsql", `SELECT oid FROM pg_language WHERE lanname='plpgsql'`, 2612},
			{"FUNCTION pg_catalog.plpgsql_call_handler()", `SELECT 'pg_catalog.plpgsql_call_handler()'::regprocedure::oid`, 1255},
			{"FUNCTION pg_catalog.plpgsql_inline_handler(internal)", `SELECT 'pg_catalog.plpgsql_inline_handler(internal)'::regprocedure::oid`, 1255},
			{"FUNCTION pg_catalog.plpgsql_validator(oid)", `SELECT 'pg_catalog.plpgsql_validator(oid)'::regprocedure::oid`, 1255},
		} {
			var n int
			q := `SELECT count(*) FROM pg_depend WHERE classid=$1 AND objid=(` + target.id + `) AND refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='plpgsql') AND deptype='e'`
			if err := c.QueryRowContext(ctx, q, target.class).Scan(&n); err != nil {
				t.Error("stock cleanup membership", err)
				continue
			}
			if n == 0 {
				if _, err := c.ExecContext(ctx, "ALTER EXTENSION plpgsql ADD "+target.sql); err != nil {
					t.Error("restoring stock membership", target.sql, err)
				}
			}
		}
	}
}
