//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// Oracle-only cases assert independently specified release behavior and remain
// runnable while the native implementation is red.
func sequenceProbeConn(t *testing.T, p *fixture.Pair) *sql.Conn {
	t.Helper()
	c, err := p.Postgres.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestReferenceSequenceProbeTransactionalRestart(t *testing.T) {
	p := fixture.NewPair(t)
	a, b := sequenceProbeConn(t, p), sequenceProbeConn(t, p)
	fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SEQUENCE s")
	fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('s')", 1)
	var pid int
	if err := b.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	// Each operation must queue behind SRX, not merely nextval cache misses.
	for _, q := range []string{"SELECT nextval('s')", "SELECT currval('s')", "SELECT lastval()", "SELECT setval('s',50)"} {
		func() {
			waitCtx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
			defer cancel()
			fixture.SequenceProbeExec(t, p.Ctx, a, "BEGIN")
			fixture.SequenceProbeExec(t, p.Ctx, a, "ALTER SEQUENCE s RESTART WITH 100")
			done := make(chan error, 1)
			go func() { var v int64; done <- b.QueryRowContext(waitCtx, q).Scan(&v) }()
			joined := false
			defer func() {
				if !joined {
					a.ExecContext(p.Ctx, "ROLLBACK")
					cancel()
					<-done
				}
			}()
			fixture.LockWait(t, p.Ctx, pid)
			// Use an independent connection: B is occupied by the queued command.
			var visible int64
			if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT last_value FROM public.s").Scan(&visible); err != nil {
				t.Fatal("SRX incorrectly blocks row reader", err)
			}
			fixture.SequenceProbeExec(t, p.Ctx, a, "ROLLBACK")
			select {
			case err := <-done:
				joined = true
				if err != nil {
					got := fixture.ExtractDiagnostic(t, err)
					if got.Code != "55000" {
						t.Fatal(err)
					}
				}
			case <-time.After(5 * time.Second):
				t.Fatal("restart waiter did not complete")
			}
		}()
	}
	fixture.SequenceProbeExec(t, p.Ctx, a, "BEGIN")
	fixture.SequenceProbeExec(t, p.Ctx, a, "ALTER SEQUENCE s RESTART 100")
	fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('s')", 100)
	fixture.SequenceProbeExec(t, p.Ctx, a, "ROLLBACK")
	fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT currval('s')", 100)
	fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT nextval('s')", 51)
	fixture.SequenceProbeExec(t, p.Ctx, a, "BEGIN")
	fixture.SequenceProbeExec(t, p.Ctx, a, "ALTER SEQUENCE s RESTART 100")
	fixture.SequenceProbeExec(t, p.Ctx, a, "COMMIT")
	fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT nextval('s')", 100)
	fixture.SequenceProbeExec(t, p.Ctx, a, "BEGIN")
	fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SEQUENCE transient")
	fixture.SequenceProbeExec(t, p.Ctx, a, "ROLLBACK")
	fixture.SequenceProbeError(t, p.Ctx, b, "SELECT nextval('transient')", nil)
	fixture.SequenceProbeExec(t, p.Ctx, a, "BEGIN")
	fixture.SequenceProbeExec(t, p.Ctx, a, "DROP SEQUENCE s")
	fixture.SequenceProbeExec(t, p.Ctx, a, "ROLLBACK")
	fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT nextval('s')", 101)
}
