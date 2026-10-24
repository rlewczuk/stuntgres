package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func transactionalSchemaNoRetarget(t *testing.T) {
	t.Run("captured_identity", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				a, c := fixture.LockConnections(t, p.Ctx, b.DB)
				fixture.LockExec(t, p.Ctx, a, "CREATE TABLE tx_identity(v INT)", 0)
				fixture.LockExec(t, p.Ctx, a, "INSERT INTO tx_identity VALUES(3)", 1)
				cp := transactionalSchemaPID(t, p, b, c)
				var oid int64
				if err := a.QueryRowContext(p.Ctx, "SELECT 'tx_identity'::regclass::oid").Scan(&oid); err != nil {
					t.Fatal(err)
				}
				stmt, err := c.PrepareContext(p.Ctx, "SELECT v FROM tx_identity")
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
				defer a.ExecContext(p.Ctx, "ROLLBACK")
				fixture.LockExec(t, p.Ctx, a, "LOCK TABLE tx_identity IN ACCESS EXCLUSIVE MODE", 0)
				done := make(chan struct{})
				await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					defer close(done)
					rows, err := stmt.QueryContext(ctx)
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				transactionalSchemaPending(t, p, b, cp, oid, done)
				fixture.LockExec(t, p.Ctx, a, "DROP TABLE tx_identity", 0)
				fixture.LockExec(t, p.Ctx, a, "CREATE TABLE tx_identity(v INT)", 0)
				fixture.LockExec(t, p.Ctx, a, "INSERT INTO tx_identity VALUES(9)", 1)
				var replacement int64
				if err := a.QueryRowContext(p.Ctx, "SELECT 'tx_identity'::regclass::oid").Scan(&replacement); err != nil || replacement == oid {
					t.Fatal(oid, replacement, err)
				}
				fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
				o := await()
				if b.PG {
					fixture.LimitLockValues(t, o, true, 9)
				} else {
					if o.Rows != nil {
						o.Rows.Close()
						t.Fatal("captured reader adopted replacement")
					}
					fixture.TransactionOrdinary(t, o.Err, "stuntgres: catalog replaced before publication")
				}
				rows, err := c.QueryContext(p.Ctx, "SELECT v FROM tx_identity")
				fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows, Err: err}, b.PG, 9)
			})
		}
	})
}
