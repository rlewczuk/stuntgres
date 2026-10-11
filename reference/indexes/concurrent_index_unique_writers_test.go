package reference_test

import (
	"context"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceConcurrentIndexSecondWritersComputed(t *testing.T) {
	for _, decorated := range []bool{false, true} {
		t.Run(fmtConcurrentCase(decorated, false, false), func(t *testing.T) {
			fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, d, c, builder := fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)
				ap, dp, bp := fixture.ConcurrentIndexPID(t, p, b, a), fixture.ConcurrentIndexPID(t, p, b, d), fixture.ConcurrentIndexPID(t, p, b, builder)
				fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(v TEXT,n INTEGER,payload TEXT)", 0)
				fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES('A',1,'seed')", 1)
				fixture.ConcurrentIndexExec(t, p, b, a, "BEGIN", 0)
				fixture.ConcurrentIndexExec(t, p, b, a, "INSERT INTO t VALUES('B',2,'first')", 1)
				keys := "lower(v)"
				detail := "Key (lower(v))=(a) already exists."
				if decorated {
					keys = "lower(v) COLLATE \"C\" text_ops DESC NULLS LAST,(n+1)"
					detail = "Key (lower(v), (n + 1))=(a, 2) already exists."
				}
				w := fixture.ConcurrentIndexStart(t, p, builder, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t("+keys+") INCLUDE(payload)", true)
				fixture.ConcurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Live: true}, bp, ap, "waiting for writers before build")
				fixture.ConcurrentIndexExec(t, p, b, d, "BEGIN", 0)
				fixture.ConcurrentIndexExec(t, p, b, d, "INSERT INTO t VALUES('a',1,'second')", 1)
				fixture.ConcurrentIndexExec(t, p, b, a, "COMMIT", 0)
				fixture.ConcurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Live: true}, bp, dp, "waiting for writers before validation")
				want := fixture.ConcurrentIndexUnique("t", "ui", detail, false)
				fixture.ConcurrentIndexError(t, p, b, c, "INSERT INTO t VALUES('a',1,'new')", want)
				fixture.ConcurrentIndexExec(t, p, b, d, "COMMIT", 0)
				fixture.AssertDiagnostic(t, w.Join(t, p), &want)
				fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES('C',3,'allowed')", 1)
				fixture.ConcurrentIndexExec(t, p, b, c, "UPDATE t SET payload='only' WHERE v='C'", 1)
				fixture.ConcurrentIndexError(t, p, b, c, "UPDATE t SET v='a',n=1 WHERE v='C'", want)
				fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 1, 1, 2, 3)
				fixture.ConcurrentIndexExec(t, p, b, c, "DROP INDEX ui", 0)
			})
		})
	}
}

func TestReferenceConcurrentIndexComputedContenders(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(fmtConcurrentCase(false, commit, false), func(t *testing.T) {
			fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, c := fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)
				fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(v TEXT,n INTEGER)", 0)
				fixture.ConcurrentIndexExec(t, p, b, c, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(lower(v) COLLATE \"C\" text_ops)", 0)
				fixture.ConcurrentIndexExec(t, p, b, a, "BEGIN", 0)
				fixture.ConcurrentIndexExec(t, p, b, a, "INSERT INTO t VALUES('A',1)", 1)
				pid := fixture.ConcurrentIndexPID(t, p, b, c)
				ctx, cancel := context.WithCancel(p.Ctx)
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
				if b.PG {
					for {
						var blocked bool
						if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT cardinality(pg_blocking_pids($1))>0", pid).Scan(&blocked); err != nil {
							t.Fatal(err)
						}
						if blocked {
							break
						}
						if p.Ctx.Err() != nil {
							t.Fatal(p.Ctx.Err())
						}
					}
				}
				end := "ROLLBACK"
				if commit {
					end = "COMMIT"
				}
				fixture.ConcurrentIndexExec(t, p, b, a, end, 0)
				var out outcome
				select {
				case out = <-done:
				case <-p.Ctx.Done():
					t.Fatal(p.Ctx.Err())
				}
				if commit {
					want := fixture.ConcurrentIndexUnique("t", "ui", "Key (lower(v))=(a) already exists.", false)
					fixture.AssertDiagnostic(t, out.err, &want)
					if out.result != nil {
						t.Fatal("failed contender result")
					}
					fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 1)
				} else {
					if out.err != nil {
						t.Fatal(out.err)
					}
					fixture.LockAffected(t, out.result, 1)
					fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 2)
				}
			})
		})
	}
}
