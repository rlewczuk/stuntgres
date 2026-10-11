package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// This shared matrix specifies SQL statement outcomes independently of backend
// parity. The internal StatementFrontier unit separately forces publication in
// the native source-opening gap; this is not a claimed PostgreSQL concurrency
// schedule or a sleep-based approximation of one.
func TestReferenceAvailableExtensionsJoinConsistency(t *testing.T) {
	availableExtensionsCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		var db *sql.DB
		for _, backend := range p.Backends() {
			if backend.PG == pg {
				db = backend.DB
			}
		}
		peer, err := db.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		queries := []string{
			"SELECT a.name,a.installed_version,v.version,v.installed,x.extversion FROM pg_catalog.pg_available_extensions a JOIN pg_catalog.pg_available_extension_versions v ON a.name=v.name LEFT JOIN pg_catalog.pg_extension x ON a.name=x.extname WHERE a.name=$1",
			"SELECT a.name,a.installed_version,v.version,v.installed,x.extversion FROM pg_catalog.pg_available_extension_versions v JOIN pg_catalog.pg_available_extensions a ON v.name=a.name LEFT JOIN pg_catalog.pg_extension x ON v.name=x.extname WHERE a.name=$1",
		}
		connections := []*sql.Conn{c, peer}
		held := make([][]fixture.HeldStatement, len(connections))
		for i, conn := range connections {
			for _, query := range queries {
				stmt, err := conn.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held[i] = append(held[i], fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query})
			}
		}
		check := func(index int, installed bool) {
			t.Helper()
			// Released view definitions join on name and exact version. Thus only
			// these two rows are possible for the genuine stock 1.1 installation.
			var version any
			if installed {
				version = "1.1"
			}
			want := [][]any{{"unaccent", version, "1.1", installed, version}}
			for j, query := range queries {
				step := fixture.Select(query, []string{"name", "installed_version", "version", "installed", "extversion"}, []string{"NAME", "TEXT", "TEXT", "BOOL", "TEXT"}, want, "unaccent")
				step.Bag = true
				availableExtensionsBoth(t, p, connections[index], r, step)
				fixture.RunStep(t, p.Ctx, held[index][j], r, step)
				step.Args = []any{"available_absent"}
				step.Rows = [][]any{}
				fixture.RunStep(t, p.Ctx, held[index][j], r, step)
			}
		}
		check(0, false)
		check(1, false)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("BEGIN", 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("SAVEPOINT before_install", 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE EXTENSION unaccent", 0))
		check(0, true)
		check(1, false)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("ROLLBACK TO before_install", 0))
		check(0, false)
		check(1, false)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE EXTENSION unaccent", 0))
		check(0, true)
		check(1, false)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("ROLLBACK", 0))
		check(0, false)
		check(1, false)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("BEGIN", 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE EXTENSION unaccent", 0))
		check(0, true)
		check(1, false)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("COMMIT", 0))
		check(0, true)
		check(1, true)
	})
}
