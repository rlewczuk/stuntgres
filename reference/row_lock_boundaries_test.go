package reference_test

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestReferenceRowLockCancellation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					a, b := referenceLockConnections(t, p.ctx, backend.db)
					referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
					referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1,10)", 1)
					ap, bp := 0, 0
					if backend.pg {
						ap, bp = referenceRowPIDs(t, p.ctx, a, b)
					}
					holder, err := a.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { holder.Rollback() })
					reader, err := b.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { reader.Rollback() })
					referenceLimitLockValues(t, referenceRowQuery(p.ctx, holder, "SELECT v FROM t FOR UPDATE", false), backend.pg, 10)
					var out relationOutcome
					if backend.pg {
						done := referenceRowAsync(t, p.ctx, reader, "SELECT v FROM t FOR KEY SHARE", prepared, true)
						referenceRowWait(t, p.ctx, bp, ap)
						var canceled bool
						if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT pg_cancel_backend($1)", bp).Scan(&canceled); err != nil || !canceled {
							t.Fatal(canceled, err)
						}
						out = referenceLockResult(t, done)
						referenceRowTimeoutDiagnostic(t, out.err, true, "57014", "canceling statement due to user request", "locking")
					} else {
						bounded, cancel := context.WithTimeout(p.ctx, 30*time.Millisecond)
						out = referenceRowQuery(bounded, reader, "SELECT v FROM t FOR KEY SHARE", prepared)
						cancel()
						if !errors.Is(out.err, context.DeadlineExceeded) {
							t.Fatal(out.err)
						}
						var state interface{ SQLState() string }
						if errors.As(out.err, &state) {
							t.Fatal("fabricated native caller SQLSTATE", out.err)
						}
					}
					if out.rows != nil {
						out.rows.Close()
						t.Fatal("canceled locker exposed rows")
					}
					if out.stmt != nil {
						if err := out.stmt.Close(); err != nil {
							t.Fatal(err)
						}
					}
					referenceRunStep(t, p.ctx, reader, backend.recorder, referenceImplicitError("SELECT v FROM t", referenceTransactionAborted()))
					if err := reader.Rollback(); err != nil {
						t.Fatal(err)
					}
					if err := holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					referenceLimitLockValues(t, referenceRowQuery(p.ctx, b, "SELECT v FROM t FOR SHARE", prepared), backend.pg, 10)
				})
			}
		})
	}
}

func TestReferenceRowLockBoundaries(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT,v INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,10)", 1))
	for _, suffix := range []string{"FOR UPDATE OF t", "FOR SHARE NOWAIT", "FOR KEY SHARE SKIP LOCKED", "FOR UPDATE FOR SHARE", "FOR UPDATE garbage"} {
		for _, prepared := range []bool{false, true} {
			out := referenceRowQuery(p.ctx, p.memory, "SELECT id FROM t "+suffix, prepared)
			if out.rows != nil {
				out.rows.Close()
				t.Fatal("excluded native clause exposed cursor", suffix)
			}
			if out.stmt != nil {
				out.stmt.Close()
				t.Fatal("excluded native Prepare returned handle", suffix)
			}
			if out.err == nil {
				t.Fatal("excluded native clause accepted", suffix)
			}
		}
	}
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("UPDATE t SET v=10", 1))
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("UPDATE t SET id=2", 1))
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
	p.step(t, referenceSelect("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SELECT v FROM t FOR SHARE", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
	if p.postgres != nil {
		for _, prepared := range []bool{false, true} {
			step := referenceImplicitError("(SELECT id FROM t UNION SELECT id FROM t) FOR UPDATE", referenceDiagnostic{Code: "0A000", Message: "FOR UPDATE is not allowed with UNION/INTERSECT/EXCEPT"})
			step.selectRows = true
			if prepared {
				step = referencePrepared(step)
			}
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, step)
		}
	}
}
