package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func preparedRebindScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	fixture.SchemaReferenceCleanup(t, "rb_early", "rb_late", "rb_missing", "rb_other", "rb_renamed", "rb_reader", "rb_reader2")
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer c.ExecContext(context.Background(), "ROLLBACK")
			defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			defer c.ExecContext(context.Background(), "DEALLOCATE ALL")
			body(t, p, b, c)
		})
	}
}

type preparedRebindRoute struct {
	fixture.Executor
	SQL string
}

func preparedRebindHold(t *testing.T, p *fixture.Pair, c *sql.Conn, query string) fixture.HeldStatement {
	t.Helper()
	s, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return fixture.HeldStatement{Executor: c, Statement: s, SQL: query}
}

// Each route owns one retained definition; RunStep must not Prepare it anew.
func preparedRebindRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query, definition, execute string) []preparedRebindRoute {
	t.Helper()
	goHeld := preparedRebindHold(t, p, c, query)
	sqlPreparedStep(t, p, b, c, fixture.Exec(definition, 0))
	return []preparedRebindRoute{{Executor: goHeld, SQL: query}, {Executor: c, SQL: execute}}
}

func preparedRebindRead(t *testing.T, p *fixture.Pair, b fixture.Backend, route preparedRebindRoute, names, types []string, rows [][]any, args ...any) {
	t.Helper()
	sqlPreparedStep(t, p, b, route, fixture.Select(route.SQL, names, types, rows, args...))
}

func preparedRebindFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, route preparedRebindRoute, d fixture.Diagnostic) {
	t.Helper()
	s := sqlPreparedError(route.SQL, d, false)
	s.SelectRows = true
	sqlPreparedStep(t, p, b, route, s)
}

func preparedRebindSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{"CREATE SCHEMA rb_early", "CREATE SCHEMA rb_late", "CREATE TABLE rb_late.t(v INT)", "SET search_path TO rb_early,rb_late"} {
		sqlPreparedStep(t, p, b, c, fixture.Exec(q, 0))
	}
	sqlPreparedStep(t, p, b, c, fixture.Exec("INSERT INTO rb_late.t VALUES(7)", 1))
}

func preparedRebindShadow(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	sqlPreparedStep(t, p, b, c, fixture.Exec("CREATE TABLE rb_early.t(v INT)", 0))
	sqlPreparedStep(t, p, b, c, fixture.Exec("INSERT INTO rb_early.t VALUES(9)", 1))
}
