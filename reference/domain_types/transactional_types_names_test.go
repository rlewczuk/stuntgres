package reference_test

import (
	"context"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalTypesNameConflicts(t *testing.T) {
	for _, family := range []string{"domain", "composite"} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/commit=%v", family, commit), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						a, c := fixture.LockConnections(t, p.Ctx, b.DB)
						fixture.LockExec(t, p.Ctx, a, "CREATE TABLE tt_unrelated(v integer)", 0)
						var ap, bp int
						var namespace int64
						if b.PG {
							ap, bp = fixture.RowPIDs(t, p.Ctx, a, c)
						}
						if err := a.QueryRowContext(p.Ctx, "SELECT oid FROM pg_namespace WHERE nspname='public'").Scan(&namespace); err != nil {
							t.Fatal(err)
						}
						q := "CREATE DOMAIN tt_same AS integer"
						if family == "composite" {
							q = "CREATE TYPE tt_same AS(v integer)"
						}
						fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
						defer a.ExecContext(p.Ctx, "ROLLBACK")
						fixture.LockExec(t, p.Ctx, a, q, 0)
						finished := make(chan struct{})
						join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
							defer close(finished)
							return fixture.RowExecute(ctx, c, q, false)
						})
						fixture.RelationLockPending(t, p, b, bp, ap, 0, "", finished)
						fixture.LockExec(t, p.Ctx, b.DB, "INSERT INTO tt_unrelated VALUES(42)", 1)
						end := "ROLLBACK"
						if commit {
							end = "COMMIT"
						}
						fixture.LockExec(t, p.Ctx, a, end, 0)
						outcome := join()
						result := outcome.Err
						if commit {
							d := fixture.ControlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "pg_type_typname_nsp_index"`)
							d.Detail = fmt.Sprintf("Key (typname, typnamespace)=(tt_same, %d) already exists.", namespace)
							d.Schema, d.Table, d.Constraint = "pg_catalog", "pg_type", "pg_type_typname_nsp_index"
							fixture.AssertDiagnostic(t, result, &d)
						} else {
							fixture.State(t, result, "")
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('tt_same','_tt_same')", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM tt_unrelated", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
					})
				}
			})
		}
	}
}
