package reference_test

import (
	"context"
	"errors"
	"testing"
)

func TestReferenceFloatingArithmeticParameters(t *testing.T) {
	referenceFloatingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		referenceFloatingRun(t, p, b, referenceExec("CREATE TABLE f(id INT,r REAL,d DOUBLE PRECISION)", 0))
		referenceFloatingRun(t, p, b, referenceExec("INSERT INTO f VALUES(1,'16777216','2')", 1))
		for _, tc := range []struct {
			q, typ, diagnostic string
			v                  float64
		}{{"SELECT r+$1 AS result FROM f", "FLOAT4", "real", 16777216}, {"SELECT $1+r AS result FROM f", "FLOAT4", "real", 16777216}, {"SELECT d+$1 AS result FROM f", "FLOAT8", "double precision", 3}, {"SELECT $1^d AS result FROM f", "FLOAT8", "double precision", 1}, {"SELECT d^$1 AS result FROM f", "FLOAT8", "double precision", 2}} {
			stmt, err := b.db.PrepareContext(p.ctx, tc.q)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer stmt.Close()
				for _, arg := range []any{1, int64(1), float64(1), "1", nil, 1} {
					want := any(tc.v)
					if arg == nil {
						want = nil
					}
					s := referenceSelect(tc.q, []string{"result"}, []string{tc.typ}, [][]any{{want}}, arg)
					referenceRunStep(t, p.ctx, referenceArithmeticMetadataExecutor{referenceHeldStatement{b.db, stmt, tc.q}, t, b.pg, s.types}, b.recorder, s)
				}
				// Both public adapters attach portal context at parameter import,
				// independently of subsequent arithmetic provider evaluation.
				want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: "invalid input syntax for type " + tc.diagnostic + ": \"oops\"", Context: "unnamed portal parameter $1 = '...'"}
				rows, err := stmt.QueryContext(p.ctx, "oops")
				err = referenceMigratedFailure(t, b.pg, rows, err)
				referenceAssertDiagnostic(t, err, &want)
				// One private execution frame must not retain either NULL or a failed import.
				referenceRunStep(t, p.ctx, referenceHeldStatement{b.db, stmt, tc.q}, b.recorder, referenceSelect(tc.q, []string{"result"}, []string{tc.typ}, [][]any{{tc.v}}, 1))
				if !b.pg {
					canceled, cancel := context.WithCancel(p.ctx)
					cancel()
					rows, err := stmt.QueryContext(canceled, 1)
					if rows != nil || !errors.Is(err, context.Canceled) {
						t.Fatal(rows, err)
					}
					referenceRunStep(t, p.ctx, referenceHeldStatement{b.db, stmt, tc.q}, b.recorder, referenceSelect(tc.q, []string{"result"}, []string{tc.typ}, [][]any{{tc.v}}, 1))
				}
			}()
		}
		for _, q := range []string{"SELECT $1^$2 FROM f", "SELECT '2'^'3' FROM f"} {
			s := referenceSelect(q, []string{"?column?"}, []string{"FLOAT8"}, [][]any{{float64(8)}})
			if q == "SELECT $1^$2 FROM f" {
				s.args = []any{2, 3}
			}
			referenceFloatingRun(t, p, b, s)
			referenceFloatingRun(t, p, b, referencePrepared(s))
		}
	})
}
