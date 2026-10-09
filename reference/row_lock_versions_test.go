package reference_test

import (
	"context"
	"testing"
	"time"
)

func referenceRowReadCommittedWaits(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, clause := range referenceRowClauses {
			for _, change := range []string{"UPDATE t SET v=20 WHERE id=1", "DELETE FROM t WHERE id=1"} {
				for _, commit := range []bool{false, true} {
					for _, predicate := range []bool{false, true} {
						t.Run(clause+"/"+change+map[bool]string{false: "/rollback", true: "/commit"}[commit]+map[bool]string{false: "/identity", true: "/predicate"}[predicate]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
							p := newReferenceScenario(t)
							// Exact resumed behavior uses a public PostgreSQL wait observer. Native
							// engine companions independently prove the same queue/version invariants.
							a, b := referenceLockConnections(t, p.ctx, p.postgres)
							referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
							referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1,10),(2,30)", 2)
							ap, bp := referenceRowPIDs(t, p.ctx, a, b)
							tx, err := a.BeginTx(p.ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer tx.Rollback()
							reader, err := b.BeginTx(p.ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer reader.Rollback()
							// Strong hold makes every clause actually wait, including KEY SHARE,
							// which is compatible with a keyless UPDATE's automatic NO KEY UPDATE.
							referenceRowValues(t, referenceRowQuery(p.ctx, tx, "SELECT v FROM t WHERE id=1 FOR UPDATE", false, referenceRawPostgres), 10)
							referenceLockExec(t, p.ctx, tx, change, 1)
							query := "SELECT v FROM t WHERE id=1 " + clause
							if predicate {
								query = "SELECT v FROM t WHERE v=10 " + clause
							}
							ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
							defer cancel()
							done := referenceRowAsync(t, ctx, reader, query, prepared, true, referenceRawPostgres)
							referenceRowWait(t, ctx, bp, ap)
							referenceLockExec(t, p.ctx, p.postgres, "INSERT INTO t VALUES(3,10)", 1)
							referenceLockExec(t, p.ctx, p.postgres, "UPDATE t SET v=10 WHERE id=2", 1)
							if commit {
								err = tx.Commit()
							} else {
								err = tx.Rollback()
							}
							if err != nil {
								t.Fatal(err)
							}
							want := []int64{10}
							if commit {
								if predicate || change[0] == 'D' {
									want = nil
								} else {
									want = []int64{20}
								}
							}
							referenceRowValues(t, referenceLockResult(t, done), want...)
							if commit && predicate && change[0] != 'D' {
								// Even an EPQ-rejected candidate remains row-locked until reader end.
								c, err := p.postgres.Conn(ctx)
								if err != nil {
									t.Fatal(err)
								}
								defer c.Close()
								var cp int
								if err := c.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&cp); err != nil {
									t.Fatal(err)
								}
								writing := referenceRowAsync(t, ctx, c, "DELETE FROM t WHERE id=1", prepared, false)
								referenceRowWait(t, ctx, cp, bp)
								reader.Rollback()
								outcome := referenceLockResult(t, writing)
								if outcome.err != nil {
									t.Fatal(outcome.err)
								}
								referenceLockAffected(t, outcome.result, 1)
							}
						})
					}
				}
			}
		}
	}
}
func referenceRowOrderingWaits(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := newReferenceScenario(t)
			a, b := referenceLockConnections(t, p.ctx, p.postgres)
			referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
			referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1,10),(2,20)", 2)
			ap, bp := referenceRowPIDs(t, p.ctx, a, b)
			tx, err := a.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			referenceLockExec(t, p.ctx, tx, "UPDATE t SET v=30 WHERE id=1", 1)
			ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
			defer cancel()
			done := referenceRowAsync(t, ctx, b, "SELECT v FROM t ORDER BY v FOR SHARE", prepared, true)
			referenceRowWait(t, ctx, bp, ap)
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			referenceLimitLockValues(t, referenceLockResult(t, done), true, 30, 20)
		})
	}
}
func TestReferenceRowLockJoins(t *testing.T) {
	p := newReferenceScenario(t)
	for _, step := range []referenceStep{referenceExec("CREATE TABLE a(id INT,v INT)", 0), referenceExec("CREATE TABLE b(id INT,v INT)", 0), referenceExec("INSERT INTO a VALUES(1,10),(1,10),(2,20)", 3), referenceExec("INSERT INTO b VALUES(1,10),(1,10),(3,30)", 3)} {
		p.step(t, step)
	}
	for _, from := range []string{"a,b", "a CROSS JOIN b", "a JOIN b ON a.id=b.id", "a JOIN b USING(id)", "a NATURAL JOIN b", "(a JOIN b USING(id)) JOIN a c USING(id)"} {
		for _, prepared := range []bool{false, true} {
			t.Run(from+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				count := 4
				if from == "(a JOIN b USING(id)) JOIN a c USING(id)" {
					count = 8
				}
				want := make([][]any, count)
				for i := range want {
					want[i] = []any{int64(10)}
				}
				step := referenceSelect("SELECT a.v FROM "+from+" WHERE a.id=1 AND b.id=1 FOR SHARE", []string{"v"}, []string{"INT4"}, want)
				step.bag = true
				if prepared {
					step = referencePrepared(step)
				}
				p.step(t, step)
			})
		}
	}
	referenceRowTextJoins(t)
	if *referenceMode {
		referenceRowJoinRecheck(t)
	}
}
func referenceRowJoinRecheck(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := newReferenceScenario(t)
			a, b := referenceLockConnections(t, p.ctx, p.postgres)
			referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
			referenceLockExec(t, p.ctx, a, "CREATE TABLE u(id INT)", 0)
			referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1,10),(2,20)", 2)
			referenceLockExec(t, p.ctx, a, "INSERT INTO u VALUES(1)", 1)
			ap, bp := referenceRowPIDs(t, p.ctx, a, b)
			tx, err := a.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			referenceLockExec(t, p.ctx, tx, "UPDATE t SET id=3 WHERE id=1", 1)
			ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
			defer cancel()
			done := referenceRowAsync(t, ctx, b, "SELECT t.v FROM t JOIN u ON t.id=u.id FOR SHARE", prepared, true, referenceRawPostgres)
			referenceRowWait(t, ctx, bp, ap)
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			referenceRowValues(t, referenceLockResult(t, done))
		})
	}
}
