package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// dsCases owns databases before roles, and all physical borrowers before either.
// All routes run the same SQL body with independently specified observations.
func dsCases(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, string)) {
	t.Helper()
	for _, route := range []string{"direct", "fresh", "held"} {
		t.Run(route, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"ds_owner", "ds_a", "ds_b"})
			dbs := []string{"ds_one", "ds_two", "Mixed db"}
			fixture.DatabaseReferenceCleanup(t, &dbs)
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					t.Cleanup(func() {
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						for _, q := range []string{"ALTER ROLE ALL RESET ALL", "REVOKE SET ON PARAMETER log_statement_stats, ds_custom.value FROM ds_owner"} {
							if _, err := b.DB.ExecContext(ctx, q); err != nil {
								t.Error(err)
							}
						}
					})
					for _, q := range []string{
						"CREATE ROLE ds_owner LOGIN PASSWORD 'role-core-fixture'",
						"CREATE ROLE ds_a LOGIN PASSWORD 'role-core-fixture'",
						"CREATE ROLE ds_b LOGIN PASSWORD 'role-core-fixture'",
						"CREATE DATABASE ds_one OWNER ds_owner", "CREATE DATABASE ds_two",
						`CREATE DATABASE "Mixed db" OWNER ds_owner`,
					} {
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
					}
					body(t, p, b, route)
				})
			}
		})
	}
}

func dsRun(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, route string, s fixture.Step) {
	t.Helper()
	// pq exposes OID as text; the native adapter exposes its numeric value.
	if !b.PG && len(s.Columns) == 3 && s.Columns[1] == "setrole" {
		for _, row := range s.Rows {
			row[1] = int64(0)
		}
	}
	// A syntax error belongs to Prepare on either prepared route, including held.
	if s.ErrorStage == "prepare" {
		s.Prepared = true
		fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
		return
	}
	rsDatabaseRun(t, p, b, e, route, s)
}

func dsPool(t *testing.T, p *fixture.Pair, b fixture.Backend, database, role string) (fixture.Backend, *sql.DB) {
	t.Helper()
	return rsDatabasePool(t, p, b, database, role)
}

func dsError(query, code, message string) fixture.Step {
	s := fixture.ImplicitError(query, fixture.ControlsReferenceDiagnostic(code, message))
	s.ErrorStage = "execute"
	return s
}

// setdatabase is checked against the actual descriptor, not an allocator golden.
// Selecting the array itself (not only a cast) also asserts TEXT[] metadata.
func dsRow(database string, config string) fixture.Step {
	rows := [][]any{}
	if config != "" {
		rows = [][]any{{true, "0", config}}
	}
	return fixture.Select(
		"SELECT s.setdatabase=d.oid AS actual_database,s.setrole,s.setconfig FROM pg_db_role_setting s JOIN pg_database d ON d.oid=s.setdatabase WHERE d.datname='"+database+"' AND s.setrole=0",
		[]string{"actual_database", "setrole", "setconfig"}, []string{"BOOL", "OID", "_TEXT"}, rows)
}

func dsFresh(t *testing.T, p *fixture.Pair, b fixture.Backend, route, database, role, value string) {
	t.Helper()
	fresh, raw := dsPool(t, p, b, database, role)
	defer fresh.DB.Close()
	defer raw.Close()
	dsRun(t, p, fresh, fresh.DB, route, rsDatabaseApplication(value))
}

func dsSetting(value, source string) fixture.Step {
	return fixture.Select("SELECT setting,reset_val,source FROM pg_settings WHERE name='application_name'",
		[]string{"setting", "reset_val", "source"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{value, value, source}})
}

func dsConn(t *testing.T, p *fixture.Pair, b fixture.Backend) *sql.Conn {
	t.Helper()
	c, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	t.Cleanup(func() { c.ExecContext(p.Ctx, "ROLLBACK") })
	return c
}
