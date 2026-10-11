package fixture

import (
	"context"
	"database/sql"
	"testing"
)

func SequenceOwnedLockPID(t *testing.T, ctx context.Context, c *sql.Conn) int {
	t.Helper()
	var pid int
	if err := c.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil || pid <= 0 {
		t.Fatal(pid, err)
	}
	return pid
}
