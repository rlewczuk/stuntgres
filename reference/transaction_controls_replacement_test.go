package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
)

func TestReferenceTransactionStaleAbortedCommit(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "retained_prepared_chain"}[prepared], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					f := newControlsReferenceFixture(t, p, backend.pg)
					ctx := p.ctx
					f.exec(t, f.b, "CREATE TABLE t(v INT NOT NULL)", false, 0)
					f.exec(t, f.b, "INSERT INTO t VALUES(77)", false, 1)
					f.exec(t, f.a, "BEGIN", false, 0)
					f.exec(t, f.a, "INSERT INTO t VALUES(1)", false, 1)
					f.rows(t, f.a, 1, 77)
					f.rows(t, f.b, 77)
					endSQL := "COMMIT"
					if prepared {
						endSQL = "COMMIT AND CHAIN"
					}
					var end *sql.Stmt
					var err error
					if prepared {
						end, err = f.a.PrepareContext(ctx, endSQL)
						if err != nil {
							t.Fatal(err)
						}
						defer end.Close()
					}
					if !backend.pg {
						if err := stuntgres.Restore(ctx, p.native, strings.NewReader("CREATE TABLE t(v INT NOT NULL);INSERT INTO t VALUES(99);")); err != nil {
							t.Fatal(err)
						}
						_, err = f.a.ExecContext(ctx, "SELECT v FROM t")
						transactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
						if prepared {
							_, err = end.ExecContext(ctx)
						} else {
							_, err = f.a.ExecContext(ctx, endSQL)
						}
						transactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
						f.rows(t, f.b, 99)
						f.exec(t, f.a, "INSERT INTO t VALUES(2)", false, 1)
						f.rows(t, f.b, 2, 99)
					} else {
						f.exec(t, f.a, "ROLLBACK", false, 0)
						f.rows(t, f.b, 77)
						f.exec(t, f.a, "INSERT INTO t VALUES(2)", false, 1)
						f.rows(t, f.b, 2, 77)
					}
				})
			}
		})
	}
}

func TestReferenceTransactionControlsReplacement(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			f := newControlsReferenceFixture(t, p, backend.pg)
			ctx := p.ctx
			f.exec(t, f.b, "CREATE TABLE t(v INT NOT NULL)", false, 0)
			f.exec(t, f.b, "INSERT INTO t VALUES(1),(77)", false, 2)
			f.exec(t, f.a, "BEGIN", false, 0)
			f.exec(t, f.a, "INSERT INTO t VALUES(4)", false, 1)
			rows, err := f.a.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			f.rows(t, f.b, 1, 77)
			if !backend.pg {
				if err := stuntgres.Restore(ctx, p.native, strings.NewReader("CREATE TABLE t(v INT NOT NULL);INSERT INTO t VALUES(99);")); err != nil {
					t.Fatal(err)
				}
			}
			referenceLimitLockValues(t, relationOutcome{rows: rows}, backend.pg, 1, 4, 77)
			if backend.pg {
				f.exec(t, f.a, "ROLLBACK", false, 0)
				f.rows(t, f.b, 1, 77)
			} else {
				_, err := f.a.ExecContext(ctx, "COMMIT AND CHAIN")
				transactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
				f.rows(t, f.b, 99)
				f.exec(t, f.a, "BEGIN", false, 0)
				f.exec(t, f.a, "INSERT INTO t VALUES(5)", false, 1)
				if err := stuntgres.Reset(ctx, p.native); err != nil {
					t.Fatal(err)
				}
				f.exec(t, f.a, "ROLLBACK AND CHAIN", false, 0)
				f.exec(t, f.a, "ROLLBACK", false, 0)
				f.exec(t, f.b, "CREATE TABLE t(v INT NOT NULL)", false, 0)
				f.exec(t, f.a, "INSERT INTO t VALUES(6)", false, 1)
				f.rows(t, f.b, 6)
			}
		})
	}
}
