//go:build reference

package fixture

import (
	"context"
	"runtime"
	"testing"
)

// Observe the exact ungranted relation request, not elapsed time or merely a
// busy backend. These expectations are independent pinned PostgreSQL outcomes.
func SchemaShadowWait(t *testing.T, ctx context.Context, pid int, relation string) {
	t.Helper()
	for {
		var waiting bool
		err := Admin.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='relation' AND relation=$2::regclass AND NOT granted) AND cardinality(pg_blocking_pids($1)) > 0`, pid, relation).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		runtime.Gosched()
	}
}
