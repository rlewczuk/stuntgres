package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCastDefinitionUtilityPolicies(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			for _, q := range []string{castDefinitionSource, castDefinitionScore} {
				run(fixture.Exec(q, 0))
			}
			for _, kind := range []string{"CREATE", "DROP"} {
				if kind == "DROP" {
					run(fixture.Exec(castDefinitionEdge, 0))
				}
				tx, err := conn.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer tx.Rollback()
					q := castDefinitionEdge
					if kind == "DROP" {
						q = "DROP CAST (cast_src AS integer)"
					}
					// Prepare alone is quiet even in a read-only transaction.
					stmt, err := tx.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatalf("read-only Prepare %s: %v", kind, err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: tx, Statement: stmt, SQL: q}
					s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("25006", "cannot execute "+kind+" CAST in a read-only transaction"))
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
				}()
				want := int64(0)
				if kind == "DROP" {
					want = 1
				}
				run(fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE castsource='cast_src'::regtype", []string{"n"}, []string{"INT8"}, [][]any{{want}}))
			}
		})
	}
}
