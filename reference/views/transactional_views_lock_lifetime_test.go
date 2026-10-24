package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalViewsLockLifetime(t *testing.T) {
	for _, release := range []bool{false, true} {
		for _, materialized := range []bool{false, true} {
			t.Run(map[bool]string{false: "rollback-to", true: "release"}[release]+"/"+map[bool]string{false: "view", true: "materialized"}[materialized], func(t *testing.T) {
				transactionalViewsRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
					if mode == "autocommit" {
						return
					}
					base := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					base(fixture.Exec("CREATE TABLE txv_source(id INT)", 0))
					base(fixture.Exec("CREATE TABLE txv_early(id INT)", 0))
					q := "CREATE VIEW txv_v AS SELECT id FROM txv_source"
					if materialized {
						q = "CREATE MATERIALIZED VIEW txv_v AS SELECT id FROM txv_source"
					}
					var x fixture.Executor = c
					if held {
						stmt, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						x = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					}
					e, finish := transactionalViewsBegin(t, p, b, c, mode)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
					run(fixture.Select("SELECT id FROM txv_early", []string{"id"}, []string{"INT4"}, nil))
					run(fixture.Exec("SAVEPOINT locks", 0))
					fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 0))
					pid, hpid := 0, 0
					var sourceOID, earlyOID int64
					if b.PG {
						pid = fixture.LockPID(t, p.Ctx, peer)
						hpid = fixture.LockPID(t, p.Ctx, c)
						sourceOID = fixture.LockOID(t, p.Ctx, "txv_source")
						earlyOID = fixture.LockOID(t, p.Ctx, "txv_early")
					}
					ptx := fixture.QueryLockBegin(t, p.Ctx, peer)
					defer ptx.Rollback()
					finished := make(chan struct{})
					join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
						defer close(finished)
						r, err := ptx.ExecContext(ctx, "LOCK TABLE txv_source IN ACCESS EXCLUSIVE MODE")
						return fixture.RelationOutcome{Result: r, Err: err}
					})
					fixture.RelationLockPending(t, p, b, pid, hpid, sourceOID, "AccessExclusiveLock", finished)
					if release {
						run(fixture.Exec("RELEASE locks", 0))
						// RELEASE retains grants: re-observe the real pending worker.
						fixture.RelationLockPending(t, p, b, pid, hpid, sourceOID, "AccessExclusiveLock", finished)
						finish(true)
					} else {
						run(fixture.Exec("ROLLBACK TO locks", 0))
					}
					if out := join(); out.Err != nil {
						t.Fatal(out.Err)
					}
					if err := ptx.Rollback(); err != nil {
						t.Fatal(err)
					}
					if !release {
						// Pre-savepoint grants survive rollback-to, until outer end.
						ptx = fixture.QueryLockBegin(t, p.Ctx, peer)
						defer ptx.Rollback()
						earlyDone := make(chan struct{})
						earlyJoin := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
							defer close(earlyDone)
							r, err := ptx.ExecContext(ctx, "LOCK TABLE txv_early IN ACCESS EXCLUSIVE MODE")
							return fixture.RelationOutcome{Result: r, Err: err}
						})
						fixture.RelationLockPending(t, p, b, pid, hpid, earlyOID, "AccessExclusiveLock", earlyDone)
						finish(false)
						if out := earlyJoin(); out.Err != nil {
							t.Fatal(out.Err)
						}
						if err := ptx.Rollback(); err != nil {
							t.Fatal(err)
						}
					}
				})
			})
		}
	}
}
