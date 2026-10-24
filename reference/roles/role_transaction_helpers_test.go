package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
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

func roleTransactionBegin(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, mode string) (fixture.Executor, func(bool)) {
	t.Helper()
	if mode == "go" {
		tx, err := c.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { tx.Rollback() })
		return tx, func(commit bool) {
			var err error
			if commit {
				err = tx.Commit()
			} else {
				err = tx.Rollback()
			}
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if mode == "sql" {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN", 0))
	}
	return c, func(commit bool) {
		if mode == "autocommit" {
			return
		}
		q := "ROLLBACK"
		if commit {
			q = "COMMIT"
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
}

func roleTransactionStep(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, held bool, s fixture.Step) {
	t.Helper()
	if !held {
		fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
		return
	}
	stmt, err := e.PrepareContext(p.Ctx, s.Query)
	if err != nil {
		t.Fatal("held Prepare", err)
	}
	defer stmt.Close()
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: e, Statement: stmt, SQL: s.Query}, b.Recorder, s)
}

func roleTransactionIdentity(session, current string) fixture.Step {
	return fixture.Select("SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{session, current}})
}

func roleTransactionExists(name string, present bool) fixture.Step {
	var count int64
	if present {
		count = 1
	}
	return fixture.Select("SELECT count(*) AS n FROM pg_catalog.pg_roles WHERE rolname='"+name+"'", []string{"n"}, []string{"INT8"}, [][]any{{count}})
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

func roleTransactionValidOID(t *testing.T, text string) {
	t.Helper()
	oid, err := strconv.ParseUint(text, 10, 32)
	if err != nil || oid == 0 {
		t.Fatal("invalid role identity", text, err)
	}
}
