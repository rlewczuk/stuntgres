package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceTransactionOptionsAndExplicitUtilityBoundary(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			a, b := referenceLockConnections(t, p.ctx, backend.db)
			run := func(x referenceExecutor, s referenceStep) { referenceRunStep(t, p.ctx, x, backend.recorder, s) }
			run(b, referenceExec("CREATE TABLE t(v INT)", 0))
			for _, level := range []sql.IsolationLevel{sql.LevelSerializable, sql.LevelSnapshot, sql.LevelLinearizable} {
				tx, err := a.BeginTx(p.ctx, &sql.TxOptions{Isolation: level})
				if level == sql.LevelSerializable {
					if err != nil || tx == nil {
						t.Fatal(tx, err)
					}
					var got string
					if err := tx.QueryRowContext(p.ctx, "SHOW transaction_isolation").Scan(&got); err != nil || got != "serializable" {
						t.Fatal(got, err)
					}
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
				} else {
					if tx != nil {
						tx.Rollback()
						t.Fatal("unsupported level returned a transaction")
					}
					want := "stuntgres: unsupported transaction isolation level"
					if backend.pg && level == sql.LevelSnapshot {
						want = "pq: isolation level not supported: 5"
					}
					if backend.pg && level == sql.LevelLinearizable {
						want = "pq: isolation level not supported: 7"
					}
					transactionOrdinary(t, err, want)
				}
			}
			tx, err := a.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			run(tx, referenceExec("INSERT INTO t VALUES(1)", 1))
			if backend.pg {
				run(tx, referenceExec("CREATE TABLE forbidden(v INT)", 0))
			} else {
				run(tx, referenceErrorDiagnostic("CREATE TABLE forbidden(v INT)", controlsReferenceDiagnostic("0A000", "statement is unsupported in an explicit transaction")))
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(b, referenceTransactionRows(false, "SELECT v FROM t ORDER BY v"))
			d := controlsReferenceDiagnostic("42P01", `relation "forbidden" does not exist`)
			d.Position = "15"
			run(b, referenceErrorDiagnostic("SELECT v FROM forbidden", d))
		})
	}
}
