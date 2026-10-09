package reference_test

import (
	"database/sql"
	"testing"
)

// SQL diagnostics are independently expected on both engines. Native fixture
// replacement is deliberately not represented as a PostgreSQL wire command.
func TestReferenceControlTransactionsDiagnostics(t *testing.T) {
	pair := newReferenceScenario(t)
	for _, backend := range pair.backends() {
		postgres := backend.pg
		t.Run(backend.name, func(t *testing.T) {
			f := newControlsReferenceFixture(t, pair, postgres)
			ctx := pair.ctx
			f.exec(t, f.b, "CREATE TABLE t(v INT NOT NULL)", false, 0)
			defer f.exec(t, f.b, "DROP TABLE t", false, 0)
			f.exec(t, f.b, "INSERT INTO t VALUES(77)", false, 1)
			for _, prepared := range []bool{false, true} {
				f.exec(t, f.a, "BEGIN", false, 0)
				f.exec(t, f.a, "INSERT INTO t VALUES(1)", prepared, 1)
				f.rows(t, f.a, 1, 77)
				f.rows(t, f.b, 77)
				syntax := controlsReferenceDiagnostic("42601", `syntax error at or near ")"`)
				syntax.Position = "27"
				for range 2 {
					var err error
					if prepared {
						var s *sql.Stmt
						s, err = f.a.PrepareContext(ctx, "SELECT * FROM t WHERE v = )")
						if s != nil {
							s.Close()
							t.Fatal("malformed Prepare returned handle")
						}
					} else {
						_, err = f.a.ExecContext(ctx, "SELECT * FROM t WHERE v = )")
					}
					referenceAssertDiagnostic(t, err, &syntax)
				}
				_, err := f.a.ExecContext(ctx, "SELECT v FROM t")
				aborted := referenceTransactionAborted()
				referenceAssertDiagnostic(t, err, &aborted)
				f.exec(t, f.a, "COMMIT", prepared, 0)
				f.rows(t, f.b, 77)
				missing := controlsReferenceDiagnostic("42P01", `relation "missing" does not exist`)
				missing.Position = "15"
				if prepared {
					s, err := f.a.PrepareContext(ctx, "SELECT v FROM missing")
					if s != nil {
						s.Close()
						t.Fatal("missing relation Prepare returned handle")
					}
					referenceAssertDiagnostic(t, err, &missing)
				} else {
					_, err = f.a.ExecContext(ctx, "SELECT v FROM missing")
					referenceAssertDiagnostic(t, err, &missing)
				}
				f.exec(t, f.a, "BEGIN", false, 0)
				f.exec(t, f.a, "INSERT INTO t VALUES(1)", false, 1)
				null := referenceTransactionNull()
				referenceAssertDiagnostic(t, transactionConstraintExec(t, ctx, f.a, "INSERT INTO t VALUES(8),(NULL)", prepared), &null)
				f.exec(t, f.a, "COMMIT AND CHAIN", prepared, 0)
				f.exec(t, f.a, "INSERT INTO t VALUES(2)", false, 1)
				f.rows(t, f.b, 77)
				f.exec(t, f.a, "ROLLBACK", false, 0)
				f.rows(t, f.b, 77)
			}
		})
	}
}
