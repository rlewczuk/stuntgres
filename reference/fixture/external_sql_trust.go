package fixture

import (
	"context"
	"strings"
	"testing"
)

// ExternalSQLTrustAuthentication installs a role-scoped real HBA trust rule in
// the disposable oracle. It does not infer authentication from configuration:
// callers must additionally observe the genuine successful handshake. Existing
// auth-file fixture ownership restores the original file before cleanup reloads.
func ExternalSQLTrustAuthentication(t *testing.T, p *Pair, role string) {
	t.Helper()
	if strings.ContainsAny(role, " \t\r\n\"\\") || role == "" {
		t.Fatal("trust fixture requires a simple role name")
	}
	if p.Postgres == nil {
		t.Fatal("trust fixture requires real PostgreSQL")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), Liveness)
		defer cancel()
		if _, err := Admin.ExecContext(ctx, "SELECT pg_reload_conf()"); err != nil {
			t.Error("restored HBA reload", err)
		}
	})
	f := &authFiles{nativeRoot: t.TempDir()}
	preparePostgresAuth(t, p, f, "hba")
	original := authExec(t, p.Ctx, "cat", f.postgres)
	f.Write(t, p.Ctx, Backend{PG: true}, "", "host all "+role+" all trust\n"+string(original))
	if _, err := Admin.ExecContext(p.Ctx, "SELECT pg_reload_conf()"); err != nil {
		t.Fatal(err)
	}
}
