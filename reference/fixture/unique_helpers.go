package fixture

import (
	"context"
	"database/sql"
	"github.com/lib/pq"
	"testing"
	"time"
)

func UniqueReferencePublicNamespace(t *testing.T, ctx context.Context, db *sql.DB, postgres bool) int64 {
	t.Helper()
	// Native Reset seeds the documented public namespace identity. The oracle
	// recreates public, so capture its real generated identity rather than assert
	// another backend's allocator or require native regnamespace/pg_namespace.
	if !postgres {
		return 2200
	}
	var oid int64
	if err := db.QueryRowContext(ctx, "SELECT oid FROM pg_namespace WHERE nspname='public'").Scan(&oid); err != nil || oid <= 0 {
		t.Fatal("public namespace", oid, err)
	}
	return oid
}

func UniqueReferenceSchemaCleanup(t *testing.T, names ...string) {
	t.Helper()
	// Register before the pair so borrowed sessions and fixture handles close
	// before admin drops schemas, and before role cleanup removes their owners.
	t.Cleanup(func() {
		if !Started {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for _, name := range names {
			if _, err := Admin.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+pq.QuoteIdentifier(name)+" CASCADE"); err != nil {
				t.Errorf("schema cleanup %s: %v", name, err)
			}
		}
	})
}
