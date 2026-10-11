package reference_test

import (
	"database/sql"
	"errors"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionControlsVisibilityAndRecovery(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			pair := fixture.NewScenario(t)
			for _, backend := range pair.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					f := fixture.NewControlsReferenceFixture(t, pair, backend.PG)
					f.Exec(t, f.B, "CREATE TABLE t(v INT NOT NULL)", false, 0)
					f.Exec(t, f.A, "BEGIN", prepared, 0)
					fixture.RunStep(t, pair.Ctx, f.A, f.Recorder, fixture.Step{Query: "INSERT INTO t VALUES($1),($2)", Prepared: true, Args: []any{int64(1), int64(2)}, Affected: 2})
					f.Rows(t, f.A, 1, 2)
					f.Rows(t, f.B)
					f.Exec(t, f.A, "END", prepared, 0)
					f.Rows(t, f.B, 1, 2)
					f.Exec(t, f.A, "START TRANSACTION", prepared, 0)
					f.Exec(t, f.A, "INSERT INTO t VALUES(3)", prepared, 1)
					f.Exec(t, f.B, "INSERT INTO t VALUES(7)", false, 1)
					f.Exec(t, f.A, "ABORT WORK", prepared, 0)
					f.Exec(t, f.A, "BEGIN TRANSACTION", prepared, 0)
					f.Exec(t, f.A, "INSERT INTO t VALUES(4)", prepared, 1)
					want := fixture.TransactionNull()
					fixture.AssertDiagnostic(t, fixture.TransactionConstraintExec(t, pair.Ctx, f.A, "INSERT INTO t VALUES(8),(NULL)", true), &want)
					_, err := f.A.ExecContext(pair.Ctx, "SELECT v FROM t")
					want = fixture.TransactionAborted()
					fixture.AssertDiagnostic(t, err, &want)
					f.Exec(t, f.A, "COMMIT", prepared, 0)
					f.NoticesWant(t, "", "")
					f.Exec(t, f.A, "INSERT INTO t VALUES(9)", true, 1)
					f.Rows(t, f.B, 1, 2, 7, 9)
				})
			}
		})
	}
}
func TestReferenceTransactionControlsChainVisibility(t *testing.T) {
	pair := fixture.NewScenario(t)
	for _, backend := range pair.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			f := fixture.NewControlsReferenceFixture(t, pair, backend.PG)
			for _, q := range []string{"COMMIT AND CHAIN", "ROLLBACK AND CHAIN", "COMMIT WORK AND CHAIN", "ABORT TRANSACTION AND CHAIN"} {
				f.Exec(t, f.B, "CREATE TABLE t(v INT NOT NULL)", false, 0)
				f.Exec(t, f.A, "BEGIN", false, 0)
				f.Exec(t, f.A, "INSERT INTO t VALUES(1)", false, 1)
				if strings.Contains(q, "WORK") || strings.HasPrefix(q, "ABORT") {
					want := fixture.TransactionNull()
					fixture.AssertDiagnostic(t, fixture.TransactionConstraintExec(t, pair.Ctx, f.A, "INSERT INTO t VALUES(NULL)", true), &want)
				}
				f.Exec(t, f.A, q, true, 0)
				f.Exec(t, f.A, "INSERT INTO t VALUES(2)", true, 1)
				want := []int64{}
				if q == "COMMIT AND CHAIN" {
					want = []int64{1}
				}
				f.Rows(t, f.B, want...)
				f.Exec(t, f.A, "ROLLBACK", false, 0)
				f.Rows(t, f.B, want...)
				f.Exec(t, f.B, "DROP TABLE t", false, 0)
			}
		})
	}
}
func TestReferenceTransactionControlsGoInterop(t *testing.T) {
	pair := fixture.NewScenario(t)
	for _, backend := range pair.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			postgres := backend.PG
			f := fixture.NewControlsReferenceFixture(t, pair, postgres)
			ctx := pair.Ctx
			f.Exec(t, f.B, "CREATE TABLE t(v INT NOT NULL)", false, 0)
			defaultTx, err := f.A.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			f.Exec(t, defaultTx, "INSERT INTO t VALUES(21)", true, 1)
			if err = defaultTx.Rollback(); err != nil {
				t.Fatal(err)
			}
			f.Rows(t, f.B)
			f.Exec(t, f.B, "DROP TABLE t", false, 0)
			for _, chain := range []bool{false, true} {
				for _, commit := range []bool{false, true} {
					f.Exec(t, f.B, "CREATE TABLE t(v INT NOT NULL)", false, 0)
					tx, err := f.A.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
					if err != nil {
						t.Fatal(err)
					}
					f.Exec(t, tx, "INSERT INTO t VALUES(13)", true, 1)
					q := "COMMIT"
					if chain {
						q += " AND CHAIN"
					}
					f.Exec(t, tx, q, false, 0)
					if !chain {
						f.Exec(t, tx, "BEGIN", false, 0)
					}
					f.Exec(t, tx, "INSERT INTO t VALUES(17)", false, 1)
					f.Rows(t, f.B, 13)
					if commit {
						err = tx.Commit()
					} else {
						err = tx.Rollback()
					}
					if postgres {
						if err != nil {
							t.Fatal("pq's unqualified end", err)
						}
					} else {
						fixture.TransactionOrdinary(t, err, "stuntgres: stale transaction reference")
					}
					if !errors.Is(tx.Commit(), sql.ErrTxDone) || !errors.Is(tx.Rollback(), sql.ErrTxDone) {
						t.Fatal("double public end")
					}
					if postgres {
						if commit {
							f.Rows(t, f.B, 13, 17)
						} else {
							f.Rows(t, f.B, 13)
						}
					} else {
						f.Rows(t, f.B, 13)
						f.Exec(t, f.A, "END", false, 0)
						f.Rows(t, f.B, 13, 17)
					}
					f.Exec(t, f.B, "DROP TABLE t", false, 0)
				}
			}
		})
	}
}
