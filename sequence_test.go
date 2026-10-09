package stuntgres_test

import (
	"context"
	"database/sql"
	"testing"
)

// These public behavioral witnesses intentionally precede production implementation.
func sequenceConn(t *testing.T, db *sql.DB) *sql.Conn {
	t.Helper()
	c, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}
func sequenceExec(t *testing.T, c *sql.Conn, q string) {
	t.Helper()
	r, err := c.ExecContext(context.Background(), q)
	if err != nil {
		t.Fatal(q, err)
	}
	n, err := r.RowsAffected()
	if err != nil || n != 0 {
		t.Fatalf("%s affected=%d err=%v", q, n, err)
	}
}
func sequenceValue(t *testing.T, c *sql.Conn, q string, want int64) {
	t.Helper()
	var got int64
	if err := c.QueryRowContext(context.Background(), q).Scan(&got); err != nil || got != want {
		t.Fatalf("%s got=%d want=%d err=%v", q, got, want, err)
	}
}
