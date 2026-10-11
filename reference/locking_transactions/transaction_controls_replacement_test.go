package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionStaleAbortedCommit(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "retained_prepared_chain"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					f := fixture.NewControlsReferenceFixture(t, p, backend.PG)
					ctx := p.Ctx
					f.Exec(t, f.B, "CREATE TABLE t(v INT NOT NULL)", false, 0)
					f.Exec(t, f.B, "INSERT INTO t VALUES(77)", false, 1)
					f.Exec(t, f.A, "BEGIN", false, 0)
					f.Exec(t, f.A, "INSERT INTO t VALUES(1)", false, 1)
					f.Rows(t, f.A, 1, 77)
					f.Rows(t, f.B, 77)
					endSQL := "COMMIT"
					if prepared {
						endSQL = "COMMIT AND CHAIN"
					}
					var end *sql.Stmt
					var err error
					if prepared {
						end, err = f.A.PrepareContext(ctx, endSQL)
						if err != nil {
							t.Fatal(err)
						}
						defer end.Close()
					}
					if !backend.PG {
						if err := stuntgres.Restore(ctx, p.Native, strings.NewReader("CREATE TABLE t(v INT NOT NULL);INSERT INTO t VALUES(99);")); err != nil {
							t.Fatal(err)
						}
						_, err = f.A.ExecContext(ctx, "SELECT v FROM t")
						fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
						if prepared {
							_, err = end.ExecContext(ctx)
						} else {
							_, err = f.A.ExecContext(ctx, endSQL)
						}
						fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
						f.Rows(t, f.B, 99)
						f.Exec(t, f.A, "INSERT INTO t VALUES(2)", false, 1)
						f.Rows(t, f.B, 2, 99)
					} else {
						f.Exec(t, f.A, "ROLLBACK", false, 0)
						f.Rows(t, f.B, 77)
						f.Exec(t, f.A, "INSERT INTO t VALUES(2)", false, 1)
						f.Rows(t, f.B, 2, 77)
					}
				})
			}
		})
	}
}

func TestReferenceTransactionControlsReplacement(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			f := fixture.NewControlsReferenceFixture(t, p, backend.PG)
			ctx := p.Ctx
			f.Exec(t, f.B, "CREATE TABLE t(v INT NOT NULL)", false, 0)
			f.Exec(t, f.B, "INSERT INTO t VALUES(1),(77)", false, 2)
			f.Exec(t, f.A, "BEGIN", false, 0)
			f.Exec(t, f.A, "INSERT INTO t VALUES(4)", false, 1)
			rows, err := f.A.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			f.Rows(t, f.B, 1, 77)
			if !backend.PG {
				if err := stuntgres.Restore(ctx, p.Native, strings.NewReader("CREATE TABLE t(v INT NOT NULL);INSERT INTO t VALUES(99);")); err != nil {
					t.Fatal(err)
				}
			}
			fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows}, backend.PG, 1, 4, 77)
			if backend.PG {
				f.Exec(t, f.A, "ROLLBACK", false, 0)
				f.Rows(t, f.B, 1, 77)
			} else {
				_, err := f.A.ExecContext(ctx, "COMMIT AND CHAIN")
				fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
				f.Rows(t, f.B, 99)
				f.Exec(t, f.A, "BEGIN", false, 0)
				f.Exec(t, f.A, "INSERT INTO t VALUES(5)", false, 1)
				if err := stuntgres.Reset(ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				f.Exec(t, f.A, "ROLLBACK AND CHAIN", false, 0)
				f.Exec(t, f.A, "ROLLBACK", false, 0)
				f.Exec(t, f.B, "CREATE TABLE t(v INT NOT NULL)", false, 0)
				f.Exec(t, f.A, "INSERT INTO t VALUES(6)", false, 1)
				f.Rows(t, f.B, 6)
			}
		})
	}
}
