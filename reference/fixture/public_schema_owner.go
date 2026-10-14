package fixture

import (
	"context"
	"strings"
	"testing"
	"time"
)

// WithPublicSchemaOwner provisions only the intended public-schema creator or
// positive ownership successor. It leaves the tested connection's identity alone
// and restores the actual previous owner before the caller resumes.
func WithPublicSchemaOwner(t *testing.T, p *Pair, backend Backend, owner string, body func()) {
	t.Helper()
	admin, err := backend.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatalf("public ownership connection: %v", err)
	}
	defer func() {
		if err := admin.Close(); err != nil {
			t.Errorf("public ownership connection close: %v", err)
		}
	}()
	for _, query := range []string{"RESET SESSION AUTHORIZATION", "RESET ROLE"} {
		if _, err := admin.ExecContext(p.Ctx, query); err != nil {
			t.Fatalf("public ownership bootstrap %s: %v", query, err)
		}
	}
	var previous string
	if err := admin.QueryRowContext(p.Ctx, "SELECT r.rolname FROM pg_catalog.pg_namespace n JOIN pg_catalog.pg_roles r ON r.oid=n.nspowner WHERE n.nspname='public'").Scan(&previous); err != nil {
		t.Fatalf("public ownership baseline: %v", err)
	}
	quote := func(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "ALTER SCHEMA public OWNER TO "+quote(previous)); err != nil {
			t.Errorf("public ownership restore to %q: %v", previous, err)
		}
	}()
	if backend.PG {
		if _, err := admin.ExecContext(p.Ctx, "GRANT USAGE ON SCHEMA public TO PUBLIC"); err != nil {
			t.Fatalf("public schema usage setup: %v", err)
		}
	}
	if _, err := admin.ExecContext(p.Ctx, "ALTER SCHEMA public OWNER TO "+quote(owner)); err != nil {
		t.Fatalf("public ownership provision to %q: %v", owner, err)
	}
	body()
}
