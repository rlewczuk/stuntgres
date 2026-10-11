package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceFloatingArithmeticParameters(t *testing.T) {
	referenceFloatingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		referenceFloatingRun(t, p, b, fixture.Exec("CREATE TABLE f(id INT,r REAL,d DOUBLE PRECISION)", 0))
		referenceFloatingRun(t, p, b, fixture.Exec("INSERT INTO f VALUES(1,'16777216','2')", 1))
		for _, tc := range []struct {
			q, typ, diagnostic string
			v                  float64
		}{{"SELECT r+$1 AS result FROM f", "FLOAT4", "real", 16777216}, {"SELECT $1+r AS result FROM f", "FLOAT4", "real", 16777216}, {"SELECT d+$1 AS result FROM f", "FLOAT8", "double precision", 3}, {"SELECT $1^d AS result FROM f", "FLOAT8", "double precision", 1}, {"SELECT d^$1 AS result FROM f", "FLOAT8", "double precision", 2}} {
			stmt, err := b.DB.PrepareContext(p.Ctx, tc.q)
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
					s := fixture.Select(tc.q, []string{"result"}, []string{tc.typ}, [][]any{{want}}, arg)
					fixture.RunStep(t, p.Ctx, referenceArithmeticMetadataExecutor{fixture.HeldStatement{b.DB, stmt, tc.q}, t, b.PG, s.Types}, b.Recorder, s)
				}
				// Both public adapters attach portal context at parameter import,
				// independently of subsequent arithmetic provider evaluation.
				want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: "invalid input syntax for type " + tc.diagnostic + ": \"oops\"", Context: "unnamed portal parameter $1 = '...'"}
				rows, err := stmt.QueryContext(p.Ctx, "oops")
				err = referenceMigratedFailure(t, b.PG, rows, err)
				fixture.AssertDiagnostic(t, err, &want)
				// One private execution frame must not retain either NULL or a failed import.
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{b.DB, stmt, tc.q}, b.Recorder, fixture.Select(tc.q, []string{"result"}, []string{tc.typ}, [][]any{{tc.v}}, 1))
				if !b.PG {
					canceled, cancel := context.WithCancel(p.Ctx)
					cancel()
					rows, err := stmt.QueryContext(canceled, 1)
					if rows != nil || !errors.Is(err, context.Canceled) {
						t.Fatal(rows, err)
					}
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{b.DB, stmt, tc.q}, b.Recorder, fixture.Select(tc.q, []string{"result"}, []string{tc.typ}, [][]any{{tc.v}}, 1))
				}
			}()
		}
		for _, q := range []string{"SELECT $1^$2 FROM f", "SELECT '2'^'3' FROM f"} {
			s := fixture.Select(q, []string{"?column?"}, []string{"FLOAT8"}, [][]any{{float64(8)}})
			if q == "SELECT $1^$2 FROM f" {
				s.Args = []any{2, 3}
			}
			referenceFloatingRun(t, p, b, s)
			referenceFloatingRun(t, p, b, fixture.Prepared(s))
		}
	})
}
