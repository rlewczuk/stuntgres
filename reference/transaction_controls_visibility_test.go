package reference_test

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func TestReferenceTransactionControlsVisibilityAndRecovery(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			pair := newReferenceScenario(t)
			for _, backend := range pair.backends() {
				t.Run(backend.name, func(t *testing.T) {
					f := newControlsReferenceFixture(t, pair, backend.pg)
					f.exec(t, f.b, "CREATE TABLE t(v INT NOT NULL)", false, 0)
					f.exec(t, f.a, "BEGIN", prepared, 0)
					referenceRunStep(t, pair.ctx, f.a, f.recorder, referenceStep{query: "INSERT INTO t VALUES($1),($2)", prepared: true, args: []any{int64(1), int64(2)}, affected: 2})
					f.rows(t, f.a, 1, 2)
					f.rows(t, f.b)
					f.exec(t, f.a, "END", prepared, 0)
					f.rows(t, f.b, 1, 2)
					f.exec(t, f.a, "START TRANSACTION", prepared, 0)
					f.exec(t, f.a, "INSERT INTO t VALUES(3)", prepared, 1)
					f.exec(t, f.b, "INSERT INTO t VALUES(7)", false, 1)
					f.exec(t, f.a, "ABORT WORK", prepared, 0)
					f.exec(t, f.a, "BEGIN TRANSACTION", prepared, 0)
					f.exec(t, f.a, "INSERT INTO t VALUES(4)", prepared, 1)
					want := referenceTransactionNull()
					referenceAssertDiagnostic(t, transactionConstraintExec(t, pair.ctx, f.a, "INSERT INTO t VALUES(8),(NULL)", true), &want)
					_, err := f.a.ExecContext(pair.ctx, "SELECT v FROM t")
					want = referenceTransactionAborted()
					referenceAssertDiagnostic(t, err, &want)
					f.exec(t, f.a, "COMMIT", prepared, 0)
					f.noticesWant(t, "", "")
					f.exec(t, f.a, "INSERT INTO t VALUES(9)", true, 1)
					f.rows(t, f.b, 1, 2, 7, 9)
				})
			}
		})
	}
}
func TestReferenceTransactionControlsChainVisibility(t *testing.T) {
	pair := newReferenceScenario(t)
	for _, backend := range pair.backends() {
		t.Run(backend.name, func(t *testing.T) {
			f := newControlsReferenceFixture(t, pair, backend.pg)
			for _, q := range []string{"COMMIT AND CHAIN", "ROLLBACK AND CHAIN", "COMMIT WORK AND CHAIN", "ABORT TRANSACTION AND CHAIN"} {
				f.exec(t, f.b, "CREATE TABLE t(v INT NOT NULL)", false, 0)
				f.exec(t, f.a, "BEGIN", false, 0)
				f.exec(t, f.a, "INSERT INTO t VALUES(1)", false, 1)
				if strings.Contains(q, "WORK") || strings.HasPrefix(q, "ABORT") {
					want := referenceTransactionNull()
					referenceAssertDiagnostic(t, transactionConstraintExec(t, pair.ctx, f.a, "INSERT INTO t VALUES(NULL)", true), &want)
				}
				f.exec(t, f.a, q, true, 0)
				f.exec(t, f.a, "INSERT INTO t VALUES(2)", true, 1)
				want := []int64{}
				if q == "COMMIT AND CHAIN" {
					want = []int64{1}
				}
				f.rows(t, f.b, want...)
				f.exec(t, f.a, "ROLLBACK", false, 0)
				f.rows(t, f.b, want...)
				f.exec(t, f.b, "DROP TABLE t", false, 0)
			}
		})
	}
}
func TestReferenceTransactionControlsGoInterop(t *testing.T) {
	pair := newReferenceScenario(t)
	for _, backend := range pair.backends() {
		t.Run(backend.name, func(t *testing.T) {
			postgres := backend.pg
			f := newControlsReferenceFixture(t, pair, postgres)
			ctx := pair.ctx
			f.exec(t, f.b, "CREATE TABLE t(v INT NOT NULL)", false, 0)
			defaultTx, err := f.a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			f.exec(t, defaultTx, "INSERT INTO t VALUES(21)", true, 1)
			if err = defaultTx.Rollback(); err != nil {
				t.Fatal(err)
			}
			f.rows(t, f.b)
			f.exec(t, f.b, "DROP TABLE t", false, 0)
			for _, chain := range []bool{false, true} {
				for _, commit := range []bool{false, true} {
					f.exec(t, f.b, "CREATE TABLE t(v INT NOT NULL)", false, 0)
					tx, err := f.a.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
					if err != nil {
						t.Fatal(err)
					}
					f.exec(t, tx, "INSERT INTO t VALUES(13)", true, 1)
					q := "COMMIT"
					if chain {
						q += " AND CHAIN"
					}
					f.exec(t, tx, q, false, 0)
					if !chain {
						f.exec(t, tx, "BEGIN", false, 0)
					}
					f.exec(t, tx, "INSERT INTO t VALUES(17)", false, 1)
					f.rows(t, f.b, 13)
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
						transactionOrdinary(t, err, "stuntgres: stale transaction reference")
					}
					if !errors.Is(tx.Commit(), sql.ErrTxDone) || !errors.Is(tx.Rollback(), sql.ErrTxDone) {
						t.Fatal("double public end")
					}
					if postgres {
						if commit {
							f.rows(t, f.b, 13, 17)
						} else {
							f.rows(t, f.b, 13)
						}
					} else {
						f.rows(t, f.b, 13)
						f.exec(t, f.a, "END", false, 0)
						f.rows(t, f.b, 13, 17)
					}
					f.exec(t, f.b, "DROP TABLE t", false, 0)
				}
			}
		})
	}
}
