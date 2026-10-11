package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionOptionsAndExplicitUtilityBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
			run := func(x fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, x, backend.Recorder, s) }
			run(b, fixture.Exec("CREATE TABLE t(v INT)", 0))
			for _, level := range []sql.IsolationLevel{sql.LevelSerializable, sql.LevelSnapshot, sql.LevelLinearizable} {
				tx, err := a.BeginTx(p.Ctx, &sql.TxOptions{Isolation: level})
				if level == sql.LevelSerializable {
					if err != nil || tx == nil {
						t.Fatal(tx, err)
					}
					var got string
					if err := tx.QueryRowContext(p.Ctx, "SHOW transaction_isolation").Scan(&got); err != nil || got != "serializable" {
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
					if backend.PG && level == sql.LevelSnapshot {
						want = "pq: isolation level not supported: 5"
					}
					if backend.PG && level == sql.LevelLinearizable {
						want = "pq: isolation level not supported: 7"
					}
					fixture.TransactionOrdinary(t, err, want)
				}
			}
			tx, err := a.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			run(tx, fixture.Exec("INSERT INTO t VALUES(1)", 1))
			if backend.PG {
				run(tx, fixture.Exec("CREATE TABLE forbidden(v INT)", 0))
			} else {
				run(tx, fixture.ErrorDiagnostic("CREATE TABLE forbidden(v INT)", fixture.ControlsReferenceDiagnostic("0A000", "statement is unsupported in an explicit transaction")))
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(b, fixture.TransactionRows(false, "SELECT v FROM t ORDER BY v"))
			d := fixture.ControlsReferenceDiagnostic("42P01", `relation "forbidden" does not exist`)
			d.Position = "15"
			run(b, fixture.ErrorDiagnostic("SELECT v FROM forbidden", d))
		})
	}
}
