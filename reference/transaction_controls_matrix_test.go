package reference_test

import (
	"database/sql"
	"strings"
	"testing"
)

func TestReferenceTransactionControlsStateMatrix(t *testing.T) {
	queries := []string{"BEGIN", "BEGIN WORK", "BEGIN TRANSACTION", "START TRANSACTION"}
	for _, word := range []string{"COMMIT", "END", "ROLLBACK", "ABORT"} {
		for _, optional := range []string{"", " WORK", " TRANSACTION"} {
			for _, suffix := range []string{"", " AND CHAIN", " AND NO CHAIN"} {
				queries = append(queries, word+optional+suffix)
			}
		}
	}
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			pair := newReferenceScenario(t)
			for _, backend := range pair.backends() {
				t.Run(backend.name, func(t *testing.T) {
					f := newControlsReferenceFixture(t, pair, backend.pg)
					ctx := pair.ctx
					for _, state := range []string{"idle", "active", "aborted"} {
						for _, q := range queries {
							t.Run(state+"/"+strings.ReplaceAll(q, " ", "_"), func(t *testing.T) {
								f.exec(t, f.b, "CREATE TABLE t(v INT NOT NULL)", false, 0)
								defer f.exec(t, f.b, "DROP TABLE t", false, 0)
								start := strings.HasPrefix(q, "BEGIN") || strings.HasPrefix(q, "START")
								commit := strings.HasPrefix(q, "COMMIT") || strings.HasPrefix(q, "END")
								chain := strings.HasSuffix(q, "AND CHAIN")
								var cached *sql.Stmt
								if prepared {
									var err error
									cached, err = f.a.PrepareContext(ctx, q)
									if err != nil {
										t.Fatal("eligible Prepare", err)
									}
									defer cached.Close()
									f.noticesWant(t, "", "")
								}
								if state != "idle" {
									f.exec(t, f.a, "BEGIN", false, 0)
									f.exec(t, f.a, "INSERT INTO t VALUES(1)", false, 1)
									if state == "aborted" {
										err := transactionConstraintExec(t, ctx, f.a, "INSERT INTO t VALUES(8),(NULL)", true)
										want := referenceTransactionNull()
										referenceAssertDiagnostic(t, err, &want)
									}
								}
								if prepared && state == "aborted" {
									s, err := f.a.PrepareContext(ctx, q)
									if start {
										want := referenceTransactionAborted()
										referenceAssertDiagnostic(t, err, &want)
										if s != nil {
											t.Fatal("failed Prepare handle")
										}
									} else {
										if err != nil {
											t.Fatal(err)
										}
										s.Close()
										_, err = f.a.ExecContext(ctx, "SELECT v FROM t")
										want := referenceTransactionAborted()
										referenceAssertDiagnostic(t, err, &want)
									}
									f.noticesWant(t, "", "")
								}
								f.rows(t, f.b)
								var r sql.Result
								var err error
								if cached != nil {
									r, err = cached.ExecContext(ctx)
								} else {
									r, err = f.a.ExecContext(ctx, q)
								}
								if start && state == "aborted" {
									want := referenceTransactionAborted()
									referenceAssertDiagnostic(t, err, &want)
									f.noticesWant(t, "", "")
									_, err = f.a.ExecContext(ctx, "SELECT v FROM t")
									referenceAssertDiagnostic(t, err, &want)
									f.rows(t, f.b)
									f.exec(t, f.a, "ROLLBACK", false, 0)
									return
								}
								if !start && state == "idle" && chain {
									word := "COMMIT"
									if !commit {
										word = "ROLLBACK"
									}
									want := controlsReferenceDiagnostic("25P01", word+" AND CHAIN can only be used in transaction blocks")
									referenceAssertDiagnostic(t, err, &want)
									f.noticesWant(t, "", "")
									f.exec(t, f.a, "INSERT INTO t VALUES(2)", false, 1)
									f.rows(t, f.b, 2)
									return
								}
								if err != nil {
									t.Fatal(q, err)
								}
								referenceLockAffected(t, r, 0)
								if start && state == "active" {
									f.noticesWant(t, "25001", "there is already a transaction in progress")
								} else if !start && state == "idle" {
									f.noticesWant(t, "25P01", "there is no transaction in progress")
								} else {
									f.noticesWant(t, "", "")
								}
								var want []int64
								if commit && state == "active" && !start {
									want = []int64{1}
								}
								f.rows(t, f.b, want...)
								f.exec(t, f.a, "INSERT INTO t VALUES(2)", true, 1)
								if start || chain {
									f.rows(t, f.b, want...)
									f.exec(t, f.a, "ROLLBACK", false, 0)
								} else {
									want = append(want, 2)
								}
								f.rows(t, f.b, want...)
							})
						}
					}
				})
			}
		})
	}
}

func TestReferenceTransactionControlsPrepareBoundaries(t *testing.T) {
	pair := newReferenceScenario(t)
	for _, backend := range pair.backends() {
		t.Run(backend.name, func(t *testing.T) {
			f := newControlsReferenceFixture(t, pair, backend.pg)
			ctx := pair.ctx
			f.exec(t, f.b, "CREATE TABLE t(v INT NOT NULL)", false, 0)
			begin, err := f.a.PrepareContext(ctx, "BEGIN")
			if err != nil {
				t.Fatal(err)
			}
			defer begin.Close()
			f.noticesWant(t, "", "")
			f.exec(t, f.a, "INSERT INTO t VALUES(7)", false, 1)
			f.rows(t, f.b, 7)
			f.exec(t, f.a, "BEGIN", false, 0)
			f.exec(t, f.a, "INSERT INTO t VALUES(1)", false, 1)
			end, err := f.a.PrepareContext(ctx, "END WORK AND CHAIN")
			if err != nil {
				t.Fatal(err)
			}
			defer end.Close()
			f.noticesWant(t, "", "")
			f.rows(t, f.b, 7)
			if _, err = begin.ExecContext(ctx); err != nil {
				t.Fatal(err)
			}
			f.noticesWant(t, "25001", "there is already a transaction in progress")
			wantNull := referenceTransactionNull()
			referenceAssertDiagnostic(t, transactionConstraintExec(t, ctx, f.a, "INSERT INTO t VALUES(8),(NULL)", true), &wantNull)
			_, err = begin.ExecContext(ctx)
			wantAborted := referenceTransactionAborted()
			referenceAssertDiagnostic(t, err, &wantAborted)
			recovery, err := f.a.PrepareContext(ctx, "COMMIT")
			if err != nil {
				t.Fatal("aborted end Prepare", err)
			}
			defer recovery.Close()
			_, err = f.a.ExecContext(ctx, "SELECT v FROM t")
			referenceAssertDiagnostic(t, err, &wantAborted)
			for _, c := range []struct{ q, message, position string }{{"COMMIT AND NO", "syntax error at end of input", "14"}, {"BEGIN AND CHAIN", `syntax error at or near "AND"`, "7"}, {"ROLLBACK WORK TRANSACTION", `syntax error at or near "TRANSACTION"`, "15"}, {"SELECT * FROM t WHERE v = )", `syntax error at or near ")"`, "27"}} {
				want := controlsReferenceDiagnostic("42601", c.message)
				want.Position = c.position
				_, err = f.a.ExecContext(ctx, c.q)
				referenceAssertDiagnostic(t, err, &want)
				s, err := f.a.PrepareContext(ctx, c.q)
				referenceAssertDiagnostic(t, err, &want)
				if s != nil {
					t.Fatal("malformed Prepare returned handle")
				}
			}
			if r, err := recovery.ExecContext(ctx); err != nil {
				t.Fatal(err)
			} else {
				referenceLockAffected(t, r, 0)
			}
			f.noticesWant(t, "", "")
			f.rows(t, f.b, 7)
			_, err = end.ExecContext(ctx)
			want := controlsReferenceDiagnostic("25P01", "COMMIT AND CHAIN can only be used in transaction blocks")
			referenceAssertDiagnostic(t, err, &want)
			if _, err = begin.ExecContext(ctx); err != nil {
				t.Fatal(err)
			}
			f.exec(t, f.a, "INSERT INTO t VALUES(9)", true, 1)
			if _, err = end.ExecContext(ctx); err != nil {
				t.Fatal(err)
			}
			f.rows(t, f.b, 7, 9)
			f.exec(t, f.a, "ROLLBACK AND NO CHAIN", false, 0)
			if _, err = recovery.ExecContext(ctx); err != nil {
				t.Fatal(err)
			}
			f.noticesWant(t, "25P01", "there is no transaction in progress")
		})
	}
}
