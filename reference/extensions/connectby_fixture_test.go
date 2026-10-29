package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations follow PostgreSQL REL_18_6 contrib/tablefunc/tablefunc.c,
// not native output. All SQL witnesses share the same backend-independent body.
func connectbyCase(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "cb_part", "cb_moved", "CB Schema")
	fixture.RoleReferenceCleanup(t, []string{"cb_reader"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if b.PG {
				clean := func() {
					for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "RESET ROLE", "DROP EXTENSION IF EXISTS tablefunc CASCADE", "DROP SCHEMA IF EXISTS cb_part CASCADE", "DROP SCHEMA IF EXISTS cb_moved CASCADE", `DROP SCHEMA IF EXISTS "CB Schema" CASCADE`} {
						if _, err := c.ExecContext(p.Ctx, q); err != nil {
							t.Error(q, err)
						}
					}
				}
				clean()
				defer clean()
			}
			fixture.ConnectbyRun(t, p, b, c, fixture.Exec("CREATE SCHEMA cb_part", 0))
			fixture.ConnectbyRun(t, p, b, c, fixture.Exec("CREATE EXTENSION tablefunc SCHEMA cb_part", 0))
			body(t, p, b, c)
		})
	}
}

func connectbyError(q, code, message, detail string) fixture.Step {
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message, Detail: detail})
	s.SelectRows = true
	s.ErrorStage = "execute"
	return s
}
func connectbyNestedError(q, sql, code, message, position string) fixture.Step {
	s := connectbyError(q, code, message, "")
	s.Diagnostic.InternalQuery = sql
	s.Diagnostic.InternalPosition = position
	if position == "" {
		s.Diagnostic.InternalQuery = ""
		s.Diagnostic.Context = "SQL statement \"" + sql + "\""
	}
	return s
}
func connectbyHold(t *testing.T, p *fixture.Pair, c *sql.Conn, q string) fixture.HeldStatement {
	t.Helper()
	s, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
}
func connectbyLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
