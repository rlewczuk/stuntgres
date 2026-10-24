package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalRulesLockLifetime(t *testing.T) {
	for _, release := range []bool{false, true} {
		for _, relation := range []string{"src", "sink1"} {
			t.Run(map[bool]string{false: "rollback-to", true: "release"}[release]+"/"+relation, func(t *testing.T) {
				transactionalRulesRoutes(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
					base := transactionalRulesRun(t, p, b, c, nil)
					transactionalRulesSetup(base)
					base(fixture.Exec("CREATE TABLE early(id INT)", 0))
					handles := transactionalRulesHandles(t, p, c, held, transactionalRulesCreate)
					pid, hpid := 0, 0
					var oid, early int64
					if b.PG {
						pid = fixture.LockPID(t, p.Ctx, peer)
						hpid = fixture.LockPID(t, p.Ctx, c)
						oid = fixture.LockOID(t, p.Ctx, relation)
						early = fixture.LockOID(t, p.Ctx, "early")
					}
					e, finish := transactionalViewsBegin(t, p, b, c, mode)
					run := transactionalRulesRun(t, p, b, e, handles)
					run(fixture.Select("SELECT id FROM early", []string{"id"}, []string{"INT4"}, nil))
					run(fixture.Exec("SAVEPOINT grants", 0))
					run(fixture.Exec(transactionalRulesCreate, 0))
					tx := fixture.QueryLockBegin(t, p.Ctx, peer)
					defer tx.Rollback()
					done := make(chan struct{})
					join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
						defer close(done)
						r, err := tx.ExecContext(ctx, "LOCK TABLE "+relation+" IN ACCESS EXCLUSIVE MODE")
						return fixture.RelationOutcome{Result: r, Err: err}
					})
					fixture.RelationLockPending(t, p, b, pid, hpid, oid, "AccessExclusiveLock", done)
					if release {
						run(fixture.Exec("RELEASE grants", 0))
						fixture.RelationLockPending(t, p, b, pid, hpid, oid, "AccessExclusiveLock", done)
						finish(true)
					} else {
						run(fixture.Exec("ROLLBACK TO grants", 0))
					}
					if out := join(); out.Err != nil {
						t.Fatal(out.Err)
					}
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					if !release {
						run(transactionalRulesNames())
						tx = fixture.QueryLockBegin(t, p.Ctx, peer)
						defer tx.Rollback()
						done = make(chan struct{})
						earlyDone := done
						join = fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
							defer close(earlyDone)
							r, err := tx.ExecContext(ctx, "LOCK TABLE early IN ACCESS EXCLUSIVE MODE")
							return fixture.RelationOutcome{Result: r, Err: err}
						})
						fixture.RelationLockPending(t, p, b, pid, hpid, early, "AccessExclusiveLock", done)
						finish(false)
						if out := join(); out.Err != nil {
							t.Fatal(out.Err)
						}
						if err := tx.Rollback(); err != nil {
							t.Fatal(err)
						}
					}
					base(fixture.Exec("INSERT INTO src VALUES(1,-10)", 1))
					var rows [][]any
					if release {
						rows = [][]any{{int64(1), int64(10)}}
					}
					base(transactionalRulesRows("sink1", rows...))
				})
			})
		}
	}
}
