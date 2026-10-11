package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
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
			pair := fixture.NewScenario(t)
			for _, backend := range pair.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					f := fixture.NewControlsReferenceFixture(t, pair, backend.PG)
					ctx := pair.Ctx
					for _, state := range []string{"idle", "active", "aborted"} {
						for _, q := range queries {
							t.Run(state+"/"+strings.ReplaceAll(q, " ", "_"), func(t *testing.T) {
								f.Exec(t, f.B, "CREATE TABLE t(v INT NOT NULL)", false, 0)
								defer f.Exec(t, f.B, "DROP TABLE t", false, 0)
								start := strings.HasPrefix(q, "BEGIN") || strings.HasPrefix(q, "START")
								commit := strings.HasPrefix(q, "COMMIT") || strings.HasPrefix(q, "END")
								chain := strings.HasSuffix(q, "AND CHAIN")
								var cached *sql.Stmt
								if prepared {
									var err error
									cached, err = f.A.PrepareContext(ctx, q)
									if err != nil {
										t.Fatal("eligible Prepare", err)
									}
									defer cached.Close()
									f.NoticesWant(t, "", "")
								}
								if state != "idle" {
									f.Exec(t, f.A, "BEGIN", false, 0)
									f.Exec(t, f.A, "INSERT INTO t VALUES(1)", false, 1)
									if state == "aborted" {
										err := fixture.TransactionConstraintExec(t, ctx, f.A, "INSERT INTO t VALUES(8),(NULL)", true)
										want := fixture.TransactionNull()
										fixture.AssertDiagnostic(t, err, &want)
									}
								}
								if prepared && state == "aborted" {
									s, err := f.A.PrepareContext(ctx, q)
									if start {
										want := fixture.TransactionAborted()
										fixture.AssertDiagnostic(t, err, &want)
										if s != nil {
											t.Fatal("failed Prepare handle")
										}
									} else {
										if err != nil {
											t.Fatal(err)
										}
										s.Close()
										_, err = f.A.ExecContext(ctx, "SELECT v FROM t")
										want := fixture.TransactionAborted()
										fixture.AssertDiagnostic(t, err, &want)
									}
									f.NoticesWant(t, "", "")
								}
								f.Rows(t, f.B)
								var r sql.Result
								var err error
								if cached != nil {
									r, err = cached.ExecContext(ctx)
								} else {
									r, err = f.A.ExecContext(ctx, q)
								}
								if start && state == "aborted" {
									want := fixture.TransactionAborted()
									fixture.AssertDiagnostic(t, err, &want)
									f.NoticesWant(t, "", "")
									_, err = f.A.ExecContext(ctx, "SELECT v FROM t")
									fixture.AssertDiagnostic(t, err, &want)
									f.Rows(t, f.B)
									f.Exec(t, f.A, "ROLLBACK", false, 0)
									return
								}
								if !start && state == "idle" && chain {
									word := "COMMIT"
									if !commit {
										word = "ROLLBACK"
									}
									want := fixture.ControlsReferenceDiagnostic("25P01", word+" AND CHAIN can only be used in transaction blocks")
									fixture.AssertDiagnostic(t, err, &want)
									f.NoticesWant(t, "", "")
									f.Exec(t, f.A, "INSERT INTO t VALUES(2)", false, 1)
									f.Rows(t, f.B, 2)
									return
								}
								if err != nil {
									t.Fatal(q, err)
								}
								fixture.LockAffected(t, r, 0)
								if start && state == "active" {
									f.NoticesWant(t, "25001", "there is already a transaction in progress")
								} else if !start && state == "idle" {
									f.NoticesWant(t, "25P01", "there is no transaction in progress")
								} else {
									f.NoticesWant(t, "", "")
								}
								var want []int64
								if commit && state == "active" && !start {
									want = []int64{1}
								}
								f.Rows(t, f.B, want...)
								f.Exec(t, f.A, "INSERT INTO t VALUES(2)", true, 1)
								if start || chain {
									f.Rows(t, f.B, want...)
									f.Exec(t, f.A, "ROLLBACK", false, 0)
								} else {
									want = append(want, 2)
								}
								f.Rows(t, f.B, want...)
							})
						}
					}
				})
			}
		})
	}
}

func TestReferenceTransactionControlsPrepareBoundaries(t *testing.T) {
	pair := fixture.NewScenario(t)
	for _, backend := range pair.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			f := fixture.NewControlsReferenceFixture(t, pair, backend.PG)
			ctx := pair.Ctx
			f.Exec(t, f.B, "CREATE TABLE t(v INT NOT NULL)", false, 0)
			begin, err := f.A.PrepareContext(ctx, "BEGIN")
			if err != nil {
				t.Fatal(err)
			}
			defer begin.Close()
			f.NoticesWant(t, "", "")
			f.Exec(t, f.A, "INSERT INTO t VALUES(7)", false, 1)
			f.Rows(t, f.B, 7)
			f.Exec(t, f.A, "BEGIN", false, 0)
			f.Exec(t, f.A, "INSERT INTO t VALUES(1)", false, 1)
			end, err := f.A.PrepareContext(ctx, "END WORK AND CHAIN")
			if err != nil {
				t.Fatal(err)
			}
			defer end.Close()
			f.NoticesWant(t, "", "")
			f.Rows(t, f.B, 7)
			if _, err = begin.ExecContext(ctx); err != nil {
				t.Fatal(err)
			}
			f.NoticesWant(t, "25001", "there is already a transaction in progress")
			wantNull := fixture.TransactionNull()
			fixture.AssertDiagnostic(t, fixture.TransactionConstraintExec(t, ctx, f.A, "INSERT INTO t VALUES(8),(NULL)", true), &wantNull)
			_, err = begin.ExecContext(ctx)
			wantAborted := fixture.TransactionAborted()
			fixture.AssertDiagnostic(t, err, &wantAborted)
			recovery, err := f.A.PrepareContext(ctx, "COMMIT")
			if err != nil {
				t.Fatal("aborted end Prepare", err)
			}
			defer recovery.Close()
			_, err = f.A.ExecContext(ctx, "SELECT v FROM t")
			fixture.AssertDiagnostic(t, err, &wantAborted)
			for _, c := range []struct{ q, message, position string }{{"COMMIT AND NO", "syntax error at end of input", "14"}, {"BEGIN AND CHAIN", `syntax error at or near "AND"`, "7"}, {"ROLLBACK WORK TRANSACTION", `syntax error at or near "TRANSACTION"`, "15"}, {"SELECT * FROM t WHERE v = )", `syntax error at or near ")"`, "27"}} {
				want := fixture.ControlsReferenceDiagnostic("42601", c.message)
				want.Position = c.position
				_, err = f.A.ExecContext(ctx, c.q)
				fixture.AssertDiagnostic(t, err, &want)
				s, err := f.A.PrepareContext(ctx, c.q)
				fixture.AssertDiagnostic(t, err, &want)
				if s != nil {
					t.Fatal("malformed Prepare returned handle")
				}
			}
			if r, err := recovery.ExecContext(ctx); err != nil {
				t.Fatal(err)
			} else {
				fixture.LockAffected(t, r, 0)
			}
			f.NoticesWant(t, "", "")
			f.Rows(t, f.B, 7)
			_, err = end.ExecContext(ctx)
			want := fixture.ControlsReferenceDiagnostic("25P01", "COMMIT AND CHAIN can only be used in transaction blocks")
			fixture.AssertDiagnostic(t, err, &want)
			if _, err = begin.ExecContext(ctx); err != nil {
				t.Fatal(err)
			}
			f.Exec(t, f.A, "INSERT INTO t VALUES(9)", true, 1)
			if _, err = end.ExecContext(ctx); err != nil {
				t.Fatal(err)
			}
			f.Rows(t, f.B, 7, 9)
			f.Exec(t, f.A, "ROLLBACK AND NO CHAIN", false, 0)
			if _, err = recovery.ExecContext(ctx); err != nil {
				t.Fatal(err)
			}
			f.NoticesWant(t, "25P01", "there is no transaction in progress")
		})
	}
}
