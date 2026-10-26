package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceAlterGeneratedExpressionLocking(t *testing.T) {
	for _, action := range []string{`SET EXPRESSION AS(a+2)`, `DROP EXPRESSION`} {
		for _, mode := range []string{"lock_timeout", "statement_timeout", "context"} {
			t.Run(action+"/"+mode, func(t *testing.T) {
				generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
					run(fixture.Exec(`CREATE TABLE i(a INT,b INT GENERATED ALWAYS AS(a+1) STORED)`, 0))
					run(fixture.Exec(`INSERT INTO i(a) VALUES(2)`, 1))
					holder := fixture.IdentityConnection(t, p.Ctx, b.DB)
					waiter, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						if err := waiter.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
							t.Error(err)
						}
					})
					observer := fixture.IdentityConnection(t, p.Ctx, b.DB)
					tx, err := holder.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec(`LOCK TABLE i IN ACCESS SHARE MODE`, 0))
					q := `ALTER TABLE i ALTER b ` + action
					if mode != "context" {
						fixture.RunStep(t, p.Ctx, waiter, b.Recorder, fixture.Exec(`SET `+mode+`='100ms'`, 0))
						d := fixture.Diagnostic{Code: "55P03", Message: "canceling statement due to lock timeout"}
						if mode == "statement_timeout" {
							d = fixture.Diagnostic{Code: "57014", Message: "canceling statement due to statement timeout"}
						}
						fixture.RunStep(t, p.Ctx, waiter, b.Recorder, fixture.ImplicitError(q, d))
						// Retire the armed physical owner; even resetting a statement
						// timeout can time out while other test packages are busy.
						if err := waiter.Raw(func(any) error { return driver.ErrBadConn }); err != driver.ErrBadConn {
							t.Fatal("retire timed owner", err)
						}
					} else {
						ctx, cancel := context.WithTimeout(p.Ctx, 10*time.Second)
						defer cancel()
						done := make(chan error, 1)
						finished := make(chan struct{})
						wp, hp := 0, 0
						var oid int64
						if b.PG {
							wp = fixture.LockPID(t, p.Ctx, waiter)
							hp = fixture.LockPID(t, p.Ctx, holder)
							oid = fixture.LockOID(t, p.Ctx, "i")
						}
						go func() { defer close(finished); _, err := waiter.ExecContext(ctx, q); done <- err }()
						defer func() {
							cancel()
							tx.Rollback()
							select {
							case <-finished:
							case <-time.After(5 * time.Second):
								t.Error("canceled ALTER worker did not join")
							}
						}()
						if b.PG {
							fixture.LockWaitBehind(t, ctx, wp, hp, oid, "AccessExclusiveLock")
						} else {
							fixture.AlterIdentityObserveAX(t, ctx, observer, done)
						}
						cancel()
						select {
						case err := <-done:
							if b.PG {
								want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
								fixture.AssertDiagnostic(t, err, &want)
							} else if !errors.Is(err, context.Canceled) {
								t.Fatal(err)
							}
						case <-time.After(5 * time.Second):
							t.Fatal("cancel did not release waiter")
						}
					}
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					run(alterGeneratedRows(`SELECT * FROM i`, []any{int64(2), int64(3)}))
					run(fixture.Exec(`INSERT INTO i(a) VALUES(4)`, 1))
					run(alterGeneratedRows(`SELECT * FROM i WHERE a=4`, []any{int64(4), int64(5)}))
					// A fresh successful AX operation proves timeout/cancel tickets were retired.
					run(fixture.Exec(`ALTER TABLE i ALTER b SET EXPRESSION AS(a+2)`, 0))
					run(alterGeneratedRows(`SELECT * FROM i WHERE a=4`, []any{int64(4), int64(6)}))
				})
			})
		}
	}
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "private-rollback", true: "private-commit"}[commit], func(t *testing.T) {
			generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec(`CREATE TABLE i(a INT,b INT GENERATED ALWAYS AS(a+1) STORED)`, 0))
				run(fixture.Exec(`INSERT INTO i(a) VALUES(2)`, 1))
				holder := fixture.IdentityConnection(t, p.Ctx, b.DB)
				peer := fixture.IdentityConnection(t, p.Ctx, b.DB)
				tx, err := holder.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec(`ALTER TABLE i ALTER b SET EXPRESSION AS(a+2)`, 0))
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, alterGeneratedRows(`SELECT * FROM i`, []any{int64(2), int64(4)}))
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec(`SET lock_timeout='100ms'`, 0))
				blocked := fixture.ImplicitError(`SELECT * FROM i`, fixture.Diagnostic{Code: "55P03", Message: "canceling statement due to lock timeout", Position: "15"})
				blocked.SelectRows = true
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, blocked)
				if commit {
					err = tx.Commit()
				} else {
					err = tx.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec(`SET lock_timeout=0`, 0))
				value := int64(3)
				if commit {
					value = 4
				}
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, alterGeneratedRows(`SELECT * FROM i`, []any{int64(2), value}))
			})
		})
	}
}
