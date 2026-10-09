package stuntgres_test

import (
	"context"
	"database/sql"
	"testing"
)

func identityConnExec(t *testing.T, ctx context.Context, c *sql.Conn, q string, count int64) {
	t.Helper()
	r, err := c.ExecContext(ctx, q)
	if err != nil {
		t.Fatal(q, err)
	}
	if n, err := r.RowsAffected(); err != nil || n != count {
		t.Fatal(q, n, err)
	}
}
