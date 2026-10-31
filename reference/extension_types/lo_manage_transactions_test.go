package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

// Keep an expected lookup error from aborting the transaction being observed.
func lmMissingInTransaction(t *testing.T, p *fixture.Pair, b fixture.Backend, tx *sql.Tx, oid int64) {
	t.Helper()
	loRun(t, p, b, tx, fixture.Exec("SAVEPOINT lm_missing_probe", 0))
	lmMissing(t, p, b, tx, oid)
	loRun(t, p, b, tx, fixture.Exec("ROLLBACK TO SAVEPOINT lm_missing_probe", 0))
	loRun(t, p, b, tx, fixture.Exec("RELEASE SAVEPOINT lm_missing_probe", 0))
}

func TestReferenceLoManageTransactions(t *testing.T) {
	lmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		lmSetup(t, p, b, c, "lo", "v", "UPDATE OR DELETE", "ROW")
		for _, q := range []string{"UPDATE lm SET v=910202", "DELETE FROM lm"} {
			tx, err := c.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer tx.Rollback()
				lmRun(t, p, b, tx, prepared, fixture.Exec(q, 1))
				lmMissingInTransaction(t, p, b, tx, 910201)
				if q == "DELETE FROM lm" {
					lmRows(t, p, b, tx)
				} else {
					lmRows(t, p, b, tx, lmRow(int64(910202), 0))
				}
				lmBytes(t, p, b, tx, 910202, "6162")
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
			}()
			lmRows(t, p, b, c, lmRow(int64(910201), 0))
			lmBytes(t, p, b, c, 910201, "00ff")
			lmBytes(t, p, b, c, 910202, "6162")
		}
		tx, err := c.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		loRun(t, p, b, tx, fixture.Exec("SAVEPOINT lm_saved", 0))
		lmRun(t, p, b, tx, prepared, fixture.Exec("UPDATE lm SET v=910202", 1))
		lmMissingInTransaction(t, p, b, tx, 910201)
		loRun(t, p, b, tx, fixture.Exec("ROLLBACK TO SAVEPOINT lm_saved", 0))
		lmRows(t, p, b, tx, lmRow(int64(910201), 0))
		lmBytes(t, p, b, tx, 910201, "00ff")
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}

		d := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "lm" violates check constraint "lm_n_check"`)
		d.Detail, d.Schema, d.Table, d.Constraint = "Failing row contains (1, 910202, -1).", "public", "lm", "lm_n_check"
		s := fixture.ErrorDiagnostic("UPDATE lm SET v=910202,n=-1 WHERE id=1", d)
		s.ErrorStage = "execute"
		lmRun(t, p, b, c, prepared, s)
		lmRows(t, p, b, c, lmRow(int64(910201), 0))
		lmBytes(t, p, b, c, 910201, "00ff")
		lmBytes(t, p, b, c, 910202, "6162")

		tx, err = c.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		lmRun(t, p, b, tx, prepared, lmError("UPDATE lm SET v=910202", "25006", "cannot execute UPDATE in a read-only transaction"))
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		if !b.PG {
			s, err := c.PrepareContext(p.Ctx, "UPDATE lm SET v=$1::oid")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			result, err := s.ExecContext(ctx, "910202")
			var state interface{ SQLState() string }
			if result != nil || !errors.Is(err, context.Canceled) || errors.As(err, &state) {
				t.Fatal("ordinary prepared cancellation", result, err)
			}
		}
		lmRows(t, p, b, c, lmRow(int64(910201), 0))
		lmBytes(t, p, b, c, 910201, "00ff")
		lmBytes(t, p, b, c, 910202, "6162")
	})
}
