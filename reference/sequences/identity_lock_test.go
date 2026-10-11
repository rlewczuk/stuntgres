package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIdentitySequenceGrantFreshness(t *testing.T) {
	for _, action := range []string{`ALTER TABLE i ALTER id SET CACHE 2`, `ALTER TABLE i ALTER id RESTART WITH 20`, `ALTER TABLE i ALTER id DROP IDENTITY`} {
		t.Run(action, func(t *testing.T) {
			fixture.AlterIdentityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec(`CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY)`, 0))
				holder := fixture.IdentityConnection(t, p.Ctx, b.DB)
				observer := fixture.IdentityConnection(t, p.Ctx, b.DB)
				var oid int64
				hp, wp := 0, 0
				if b.PG {
					oid = fixture.LockOID(t, p.Ctx, "i_id_seq")
					hp = fixture.LockPID(t, p.Ctx, holder)
					wp = fixture.LockPID(t, p.Ctx, c)
				}
				tx, err := holder.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec(`ALTER SEQUENCE i_id_seq INCREMENT 3 RESTART WITH 10`, 0))
				ctx, cancel := context.WithCancel(p.Ctx)
				defer cancel()
				done := make(chan error, 1)
				go func() {
					r, e := c.ExecContext(ctx, action)
					if e == nil {
						var n int64
						n, e = r.RowsAffected()
						if e == nil && n != 0 {
							e = fmt.Errorf("ALTER count=%d want 0", n)
						}
					}
					done <- e
				}()
				joined := false
				defer func() {
					cancel()
					tx.Rollback()
					if !joined {
						select {
						case <-done:
						case <-p.Ctx.Done():
							t.Error("freshness worker did not join")
						}
					}
				}()
				if b.PG {
					mode := "ShareRowExclusiveLock"
					if action == `ALTER TABLE i ALTER id DROP IDENTITY` {
						mode = "AccessExclusiveLock"
					}
					fixture.LockWaitBehind(t, p.Ctx, wp, hp, oid, mode)
				} else {
					fixture.AlterIdentityObserveAX(t, p.Ctx, observer, done)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-done:
					joined = true
					if err != nil {
						t.Fatal(err)
					}
				case <-p.Ctx.Done():
					t.Fatal(p.Ctx.Err())
				}
				if action == `ALTER TABLE i ALTER id DROP IDENTITY` {
					run(fixture.Exec(`INSERT INTO i VALUES(55)`, 1))
					fixture.AlterIdentityMissing(t, p, b, c, "i_id_seq")
				} else {
					want := int64(10)
					if action == `ALTER TABLE i ALTER id RESTART WITH 20` {
						want = 20
					}
					run(fixture.Exec(`INSERT INTO i DEFAULT VALUES`, 1))
					run(fixture.Exec(`INSERT INTO i DEFAULT VALUES`, 1))
					run(fixture.Select(`SELECT * FROM i ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{want}, {want + 3}}))
					if b.PG {
						var increment int64
						if err := c.QueryRowContext(p.Ctx, `SELECT increment_by FROM pg_sequences WHERE schemaname='public' AND sequencename='i_id_seq'`).Scan(&increment); err != nil || increment != 3 {
							t.Fatal(increment, err)
						}
					}
				}
			})
		})
	}
}
