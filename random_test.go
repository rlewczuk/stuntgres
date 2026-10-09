package stuntgres_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres"
)

func randomOne(t *testing.T, c *sql.Conn, q string, args ...any) any {
	t.Helper()
	var v any
	if err := c.QueryRowContext(context.Background(), q, args...).Scan(&v); err != nil {
		t.Fatal(q, err)
	}
	return v
}
func randomSeed(t *testing.T, c *sql.Conn, seed float64) {
	t.Helper()
	if v := randomOne(t, c, "SELECT setseed($1)", seed); v != "" {
		t.Fatalf("nonnull VOID: %T %v", v, v)
	}
}
func randomConnection(t *testing.T) (*sql.DB, *sql.Conn) {
	t.Helper()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	c, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return db, c
}
