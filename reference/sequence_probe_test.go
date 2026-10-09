//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// Oracle-only cases assert independently specified release behavior and remain
// runnable while the native implementation is red.
func sequenceProbeConn(t *testing.T, p *referencePair) *sql.Conn {
	t.Helper()
	c, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestReferenceSequenceProbeTransactionalRestart(t *testing.T) {
	p := newReferencePair(t)
	a, b := sequenceProbeConn(t, p), sequenceProbeConn(t, p)
	sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE s")
	sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 1)
	var pid int
	if err := b.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	// Each operation must queue behind SRX, not merely nextval cache misses.
	for _, q := range []string{"SELECT nextval('s')", "SELECT currval('s')", "SELECT lastval()", "SELECT setval('s',50)"} {
		func() {
			waitCtx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
			defer cancel()
			sequenceProbeExec(t, p.ctx, a, "BEGIN")
			sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE s RESTART WITH 100")
			done := make(chan error, 1)
			go func() { var v int64; done <- b.QueryRowContext(waitCtx, q).Scan(&v) }()
			joined := false
			defer func() {
				if !joined {
					a.ExecContext(p.ctx, "ROLLBACK")
					cancel()
					<-done
				}
			}()
			referenceLockWait(t, p.ctx, pid)
			// Use an independent connection: B is occupied by the queued command.
			var visible int64
			if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT last_value FROM public.s").Scan(&visible); err != nil {
				t.Fatal("SRX incorrectly blocks row reader", err)
			}
			sequenceProbeExec(t, p.ctx, a, "ROLLBACK")
			select {
			case err := <-done:
				joined = true
				if err != nil {
					got := referenceExtractDiagnostic(t, err)
					if got.Code != "55000" {
						t.Fatal(err)
					}
				}
			case <-time.After(5 * time.Second):
				t.Fatal("restart waiter did not complete")
			}
		}()
	}
	sequenceProbeExec(t, p.ctx, a, "BEGIN")
	sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE s RESTART 100")
	sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 100)
	sequenceProbeExec(t, p.ctx, a, "ROLLBACK")
	sequenceProbeValue(t, p.ctx, a, "SELECT currval('s')", 100)
	sequenceProbeValue(t, p.ctx, b, "SELECT nextval('s')", 51)
	sequenceProbeExec(t, p.ctx, a, "BEGIN")
	sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE s RESTART 100")
	sequenceProbeExec(t, p.ctx, a, "COMMIT")
	sequenceProbeValue(t, p.ctx, b, "SELECT nextval('s')", 100)
	sequenceProbeExec(t, p.ctx, a, "BEGIN")
	sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE transient")
	sequenceProbeExec(t, p.ctx, a, "ROLLBACK")
	sequenceProbeError(t, p.ctx, b, "SELECT nextval('transient')", nil)
	sequenceProbeExec(t, p.ctx, a, "BEGIN")
	sequenceProbeExec(t, p.ctx, a, "DROP SEQUENCE s")
	sequenceProbeExec(t, p.ctx, a, "ROLLBACK")
	sequenceProbeValue(t, p.ctx, b, "SELECT nextval('s')", 101)
}
