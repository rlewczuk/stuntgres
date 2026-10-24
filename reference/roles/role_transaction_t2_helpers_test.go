package reference_test

import (
	"context"
	"database/sql"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func roleTransactionT2Cases(t *testing.T, names, schemas []string, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, names)
	for _, schema := range schemas {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func roleTransactionT2Conn(t *testing.T, p *fixture.Pair, b fixture.Backend) (*sql.Conn, int) {
	t.Helper()
	c, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	t.Cleanup(func() { c.ExecContext(p.Ctx, "ROLLBACK") })
	return c, roleTransactionT2PID(t, p, b, c)
}

func roleTransactionT2PID(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) int {
	t.Helper()
	if b.PG {
		return fixture.LockPID(t, p.Ctx, c)
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TEMP SEQUENCE role_tx_physical_identity", 0))
	var name string
	if err := c.QueryRowContext(p.Ctx, "SELECT nspname FROM pg_namespace WHERE oid=pg_my_temp_schema()").Scan(&name); err != nil {
		t.Fatal(err)
	}
	text, ok := strings.CutPrefix(name, "pg_temp_")
	pid, err := strconv.ParseUint(text, 10, 31)
	if !ok || err != nil || pid == 0 {
		t.Fatal("physical identity", name, err)
	}
	return int(pid)
}

func roleTransactionT2Wait(t *testing.T, p *fixture.Pair, b fixture.Backend, waiter, holder int, completed <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cancel()
	for {
		var blocked bool
		if err := b.DB.QueryRowContext(ctx, "SELECT $1=ANY(pg_blocking_pids($2))", holder, waiter).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		select {
		case <-completed:
			t.Fatal("operation completed before the required role-lock wait")
		default:
		}
		if err := ctx.Err(); err != nil {
			t.Fatal(err)
		}
		runtime.Gosched()
	}
}

func roleTransactionT2Worker(t *testing.T, p *fixture.Pair, c fixture.Executor, query string, held bool) (func() fixture.RelationOutcome, <-chan struct{}) {
	t.Helper()
	completed := make(chan struct{})
	join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
		defer close(completed)
		return fixture.RowExecute(ctx, c, query, held)
	})
	return join, completed
}

func roleTransactionT2Success(t *testing.T, outcome fixture.RelationOutcome) {
	t.Helper()
	if outcome.Err != nil {
		t.Fatal(outcome.Err)
	}
	fixture.LockAffected(t, outcome.Result, 0)
}
