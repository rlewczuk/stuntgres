package reference_test

import (
	"context"
	"database/sql"
	"stuntgres"
	"testing"
	"time"
)

func TestReferenceConcurrentIndexSecondWritersComputed(t *testing.T) {
	for _, decorated := range []bool{false, true} {
		t.Run(fmtConcurrentCase(decorated, false, false), func(t *testing.T) {
			concurrentIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				a, d, c, builder := concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b)
				ap, dp, bp := concurrentIndexPID(t, p, b, a), concurrentIndexPID(t, p, b, d), concurrentIndexPID(t, p, b, builder)
				concurrentIndexExec(t, p, b, c, "CREATE TABLE t(v TEXT,n INTEGER,payload TEXT)", 0)
				concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES('A',1,'seed')", 1)
				concurrentIndexExec(t, p, b, a, "BEGIN", 0)
				concurrentIndexExec(t, p, b, a, "INSERT INTO t VALUES('B',2,'first')", 1)
				keys := "lower(v)"
				detail := "Key (lower(v))=(a) already exists."
				if decorated {
					keys = "lower(v) COLLATE \"C\" text_ops DESC NULLS LAST,(n+1)"
					detail = "Key (lower(v), (n + 1))=(a, 2) already exists."
				}
				w := concurrentIndexStart(t, p, builder, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t("+keys+") INCLUDE(payload)", true)
				concurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Live: true}, bp, ap, "waiting for writers before build")
				concurrentIndexExec(t, p, b, d, "BEGIN", 0)
				concurrentIndexExec(t, p, b, d, "INSERT INTO t VALUES('a',1,'second')", 1)
				concurrentIndexExec(t, p, b, a, "COMMIT", 0)
				concurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Live: true}, bp, dp, "waiting for writers before validation")
				want := concurrentIndexUnique("t", "ui", detail, false)
				concurrentIndexError(t, p, b, c, "INSERT INTO t VALUES('a',1,'new')", want)
				concurrentIndexExec(t, p, b, d, "COMMIT", 0)
				referenceAssertDiagnostic(t, w.join(t, p), &want)
				concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES('C',3,'allowed')", 1)
				concurrentIndexExec(t, p, b, c, "UPDATE t SET payload='only' WHERE v='C'", 1)
				concurrentIndexError(t, p, b, c, "UPDATE t SET v='a',n=1 WHERE v='C'", want)
				concurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 1, 1, 2, 3)
				concurrentIndexExec(t, p, b, c, "DROP INDEX ui", 0)
			})
		})
	}
}

func TestReferenceConcurrentIndexComputedContenders(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(fmtConcurrentCase(false, commit, false), func(t *testing.T) {
			concurrentIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				a, c := concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b)
				concurrentIndexExec(t, p, b, c, "CREATE TABLE t(v TEXT,n INTEGER)", 0)
				concurrentIndexExec(t, p, b, c, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(lower(v) COLLATE \"C\" text_ops)", 0)
				concurrentIndexExec(t, p, b, a, "BEGIN", 0)
				concurrentIndexExec(t, p, b, a, "INSERT INTO t VALUES('A',1)", 1)
				pid := concurrentIndexPID(t, p, b, c)
				ctx, cancel := context.WithCancel(p.ctx)
				defer cancel()
				type outcome struct {
					result sql.Result
					err    error
				}
				done := make(chan outcome, 1)
				go func() { r, e := c.ExecContext(ctx, "INSERT INTO t VALUES('a',2)"); done <- outcome{r, e}; close(done) }()
				t.Cleanup(func() {
					cancel()
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("contender did not join")
					}
				})
				if b.pg {
					for {
						var blocked bool
						if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT cardinality(pg_blocking_pids($1))>0", pid).Scan(&blocked); err != nil {
							t.Fatal(err)
						}
						if blocked {
							break
						}
						if p.ctx.Err() != nil {
							t.Fatal(p.ctx.Err())
						}
					}
				}
				end := "ROLLBACK"
				if commit {
					end = "COMMIT"
				}
				concurrentIndexExec(t, p, b, a, end, 0)
				var out outcome
				select {
				case out = <-done:
				case <-p.ctx.Done():
					t.Fatal(p.ctx.Err())
				}
				if commit {
					want := concurrentIndexUnique("t", "ui", "Key (lower(v))=(a) already exists.", false)
					referenceAssertDiagnostic(t, out.err, &want)
					if out.result != nil {
						t.Fatal("failed contender result")
					}
					concurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 1)
				} else {
					if out.err != nil {
						t.Fatal(out.err)
					}
					referenceLockAffected(t, out.result, 1)
					concurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 2)
				}
			})
		})
	}
}
