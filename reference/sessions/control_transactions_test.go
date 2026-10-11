package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// SQL diagnostics are independently expected on both engines. Native fixture
// replacement is deliberately not represented as a PostgreSQL wire command.
func TestReferenceControlTransactionsDiagnostics(t *testing.T) {
	pair := fixture.NewScenario(t)
	for _, backend := range pair.Backends() {
		postgres := backend.PG
		t.Run(backend.Name, func(t *testing.T) {
			f := fixture.NewControlsReferenceFixture(t, pair, postgres)
			ctx := pair.Ctx
			f.Exec(t, f.B, "CREATE TABLE t(v INT NOT NULL)", false, 0)
			defer f.Exec(t, f.B, "DROP TABLE t", false, 0)
			f.Exec(t, f.B, "INSERT INTO t VALUES(77)", false, 1)
			for _, prepared := range []bool{false, true} {
				f.Exec(t, f.A, "BEGIN", false, 0)
				f.Exec(t, f.A, "INSERT INTO t VALUES(1)", prepared, 1)
				f.Rows(t, f.A, 1, 77)
				f.Rows(t, f.B, 77)
				syntax := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near ")"`)
				syntax.Position = "27"
				for range 2 {
					var err error
					if prepared {
						var s *sql.Stmt
						s, err = f.A.PrepareContext(ctx, "SELECT * FROM t WHERE v = )")
						if s != nil {
							s.Close()
							t.Fatal("malformed Prepare returned handle")
						}
					} else {
						_, err = f.A.ExecContext(ctx, "SELECT * FROM t WHERE v = )")
					}
					fixture.AssertDiagnostic(t, err, &syntax)
				}
				_, err := f.A.ExecContext(ctx, "SELECT v FROM t")
				aborted := fixture.TransactionAborted()
				fixture.AssertDiagnostic(t, err, &aborted)
				f.Exec(t, f.A, "COMMIT", prepared, 0)
				f.Rows(t, f.B, 77)
				missing := fixture.ControlsReferenceDiagnostic("42P01", `relation "missing" does not exist`)
				missing.Position = "15"
				if prepared {
					s, err := f.A.PrepareContext(ctx, "SELECT v FROM missing")
					if s != nil {
						s.Close()
						t.Fatal("missing relation Prepare returned handle")
					}
					fixture.AssertDiagnostic(t, err, &missing)
				} else {
					_, err = f.A.ExecContext(ctx, "SELECT v FROM missing")
					fixture.AssertDiagnostic(t, err, &missing)
				}
				f.Exec(t, f.A, "BEGIN", false, 0)
				f.Exec(t, f.A, "INSERT INTO t VALUES(1)", false, 1)
				null := fixture.TransactionNull()
				fixture.AssertDiagnostic(t, fixture.TransactionConstraintExec(t, ctx, f.A, "INSERT INTO t VALUES(8),(NULL)", prepared), &null)
				f.Exec(t, f.A, "COMMIT AND CHAIN", prepared, 0)
				f.Exec(t, f.A, "INSERT INTO t VALUES(2)", false, 1)
				f.Rows(t, f.B, 77)
				f.Exec(t, f.A, "ROLLBACK", false, 0)
				f.Rows(t, f.B, 77)
			}
		})
	}
}
