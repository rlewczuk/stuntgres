package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGProcDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			const valid = "SELECT * FROM pg_catalog.pg_proc WHERE oid=$1::oid"
			stmt, err := b.DB.PrepareContext(p.Ctx, valid)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: valid}
			for _, tc := range []struct{ q, code, message, position string }{
				{"SELECT missing FROM pg_catalog.pg_proc", "42703", `column "missing" does not exist`, "8"},
				{"SELECT * FROM pg_catalog.pg_proc_absent", "42P01", `relation "pg_catalog.pg_proc_absent" does not exist`, "15"},
				{"SELECT /*é*/ missing FROM pg_catalog.pg_proc", "42703", `column "missing" does not exist`, "14"},
			} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
					s.SelectRows, s.Prepared = true, prepared
					if prepared {
						s.ErrorStage = "prepare"
					} else {
						s.ErrorStage = "execute"
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, pgProcShapeStep(b, valid, [][]any{pgProcAbsRow()}, int64(1397)))
				}
			}
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, prepared := range []bool{false, true} {
				var failure error
				if prepared {
					rows, err := stmt.QueryContext(canceled, int64(1397))
					failure = err
					if rows != nil {
						rows.Close()
						t.Fatal("canceled held query exposed rows")
					}
				} else {
					rows, err := b.DB.QueryContext(canceled, valid, int64(1397))
					failure = err
					if rows != nil {
						rows.Close()
						t.Fatal("canceled direct query exposed rows")
					}
				}
				if !errors.Is(failure, context.Canceled) {
					t.Fatalf("ordinary cancellation: %v", failure)
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, pgProcShapeStep(b, valid, [][]any{pgProcAbsRow()}, int64(1397)))
			}
			// Invalidate a held public query via actual DDL, then recover it. This
			// exercises normal relation binding, not synthetic pg_proc invalidation.
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			pgProcExec(t, p, b, c, "CREATE TABLE pp_diag_gate(oid oid)")
			const gated = "SELECT p.proname FROM pg_proc p JOIN pp_diag_gate g ON p.oid=g.oid"
			gate, err := c.PrepareContext(p.Ctx, gated)
			if err != nil {
				t.Fatal(err)
			}
			defer gate.Close()
			pgProcExec(t, p, b, c, "DROP TABLE pp_diag_gate")
			rows, err := gate.QueryContext(p.Ctx)
			if rows != nil {
				rows.Close()
				t.Fatal("invalidated held query exposed rows")
			}
			d := fixture.ControlsReferenceDiagnostic("42P01", `relation "pp_diag_gate" does not exist`)
			d.Position = "38"
			fixture.AssertDiagnostic(t, err, &d)
			pgProcExec(t, p, b, c, "CREATE TABLE pp_diag_gate(oid oid)")
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: gate, SQL: gated}, b.Recorder, fixture.Select(gated, []string{"proname"}, []string{"NAME"}, nil))
		})
	}
}
