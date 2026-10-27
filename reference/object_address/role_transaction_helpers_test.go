package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// Each cell owns a fresh fixture and physical owner; no matrix shares a deadline.
func roleTransactionCases(t *testing.T, names, schemas []string, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, bool, string, bool)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		for _, mode := range []string{"autocommit", "sql", "go"} {
			for _, commit := range []bool{false, true} {
				if mode == "autocommit" && !commit {
					continue
				}
				t.Run(fmt.Sprintf("held=%v/%s/commit=%v", prepared, mode, commit), func(t *testing.T) {
					fixture.RoleReferenceCleanup(t, names)
					for _, schema := range schemas {
						fixture.SchemaReferenceCleanup(t, schema)
					}
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							c, err := b.DB.Conn(p.Ctx)
							if err != nil {
								t.Fatal(err)
							}
							t.Cleanup(func() { c.Close() })
							t.Cleanup(func() { c.ExecContext(p.Ctx, "ROLLBACK") })
							body(t, p, b, c, prepared, mode, commit)
						})
					}
				})
			}
		}
	}
}

func roleTransactionLoginPool(t *testing.T, p *fixture.Pair, b fixture.Backend, name string) *sql.DB {
	t.Helper()
	if b.PG {
		// Match RoleScenarioLoginPool's oracle credential without recording it.
		if _, err := fixture.Admin.ExecContext(p.Ctx, "ALTER ROLE "+name+" PASSWORD 'role-core-fixture'"); err != nil {
			t.Fatal(err)
		}
	}
	var database string
	if err := b.DB.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
		t.Fatal(err)
	}
	// RunStep requires the same recording middleware as the scenario pool.
	pool, _, _ := fixture.DatabaseScenarioPool(t, p, b.PG, database, name)
	return pool
}
