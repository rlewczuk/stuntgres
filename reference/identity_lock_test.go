package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceIdentitySequenceGrantFreshness(t *testing.T) {
	for _, action := range []string{`ALTER TABLE i ALTER id SET CACHE 2`, `ALTER TABLE i ALTER id RESTART WITH 20`, `ALTER TABLE i ALTER id DROP IDENTITY`} {
		t.Run(action, func(t *testing.T) {
			alterIdentityScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
				run(referenceExec(`CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY)`, 0))
				holder := identityConnection(t, p.ctx, b.db)
				observer := identityConnection(t, p.ctx, b.db)
				var oid int64
				hp, wp := 0, 0
				if b.pg {
					oid = referenceLockOID(t, p.ctx, "i_id_seq")
					hp = referenceLockPID(t, p.ctx, holder)
					wp = referenceLockPID(t, p.ctx, c)
				}
				tx, err := holder.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec(`ALTER SEQUENCE i_id_seq INCREMENT 3 RESTART WITH 10`, 0))
				ctx, cancel := context.WithCancel(p.ctx)
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
						case <-p.ctx.Done():
							t.Error("freshness worker did not join")
						}
					}
				}()
				if b.pg {
					mode := "ShareRowExclusiveLock"
					if action == `ALTER TABLE i ALTER id DROP IDENTITY` {
						mode = "AccessExclusiveLock"
					}
					referenceLockWaitBehind(t, p.ctx, wp, hp, oid, mode)
				} else {
					alterIdentityObserveAX(t, p.ctx, observer, done)
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
				case <-p.ctx.Done():
					t.Fatal(p.ctx.Err())
				}
				if action == `ALTER TABLE i ALTER id DROP IDENTITY` {
					run(referenceExec(`INSERT INTO i VALUES(55)`, 1))
					alterIdentityMissing(t, p, b, c, "i_id_seq")
				} else {
					want := int64(10)
					if action == `ALTER TABLE i ALTER id RESTART WITH 20` {
						want = 20
					}
					run(referenceExec(`INSERT INTO i DEFAULT VALUES`, 1))
					run(referenceExec(`INSERT INTO i DEFAULT VALUES`, 1))
					run(referenceSelect(`SELECT * FROM i ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{want}, {want + 3}}))
					if b.pg {
						var increment int64
						if err := c.QueryRowContext(p.ctx, `SELECT increment_by FROM pg_sequences WHERE schemaname='public' AND sequencename='i_id_seq'`).Scan(&increment); err != nil || increment != 3 {
							t.Fatal(increment, err)
						}
					}
				}
			})
		})
	}
}
