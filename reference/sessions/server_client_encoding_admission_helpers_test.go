//go:build !noservertests

package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// Move an actual default out of every session's search path. Keep the original
// object alive and register repair before mutation, including failed assertions.
func clientEncodingRetireSeed(t *testing.T, ctx context.Context, pool *sql.DB, seed, hidden string) func() {
	t.Helper()
	if _, err := pool.ExecContext(ctx, "CREATE SCHEMA "+hidden); err != nil {
		t.Fatal(err)
	}
	var oid int64
	if err := pool.QueryRowContext(ctx, "SELECT oid FROM pg_conversion WHERE conname=$1", seed).Scan(&oid); err != nil {
		t.Fatal(err)
	}
	restore := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var namespace int64
		if err := pool.QueryRowContext(ctx, "SELECT connamespace::bigint FROM pg_conversion WHERE oid=$1", oid).Scan(&namespace); err != nil {
			t.Error("locate retired seed", err)
			return
		}
		if namespace != 11 {
			if _, err := pool.ExecContext(ctx, "ALTER CONVERSION "+hidden+"."+seed+" SET SCHEMA pg_catalog"); err != nil {
				t.Error("restore retired seed", err)
				return
			}
		}
		if _, err := pool.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+hidden); err != nil {
			t.Error("drop retired-seed namespace", err)
		}
	}
	t.Cleanup(restore)
	if _, err := pool.ExecContext(ctx, "ALTER CONVERSION pg_catalog."+seed+" SET SCHEMA "+hidden); err != nil {
		t.Fatal(err)
	}
	return restore
}
