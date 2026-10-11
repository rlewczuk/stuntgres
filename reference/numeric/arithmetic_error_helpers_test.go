package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// Arithmetic writes must not publish a result when analysis or execution fails.
// Keep this public-interface assertion beside the migrated consumer scenarios.
type referenceArithmeticExecutor struct {
	fixture.Executor
	t *testing.T
}

func (e referenceArithmeticExecutor) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	result, err := e.Executor.ExecContext(ctx, query, args...)
	if err != nil && result != nil {
		e.t.Fatalf("failed Exec exposed a result: %s: %v", query, err)
	}
	return result, err
}

func referenceArithmeticFailure(t *testing.T, p *fixture.Pair, step fixture.Step) {
	t.Helper()
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			step := step
			if backend.PG && step.PostgresDiagnostic != nil {
				step.State = step.PostgresDiagnostic.Code
			}
			if step.Prepared && !step.SelectRows && step.ErrorStage == "execute" {
				stmt, err := backend.DB.PrepareContext(p.Ctx, step.Query)
				if err != nil {
					t.Fatal("execution failure moved into Prepare", err)
				}
				defer func() {
					if err := stmt.Close(); err != nil {
						t.Error(err)
					}
				}()
				step.Prepared = false
				held := fixture.HeldStatement{backend.DB, stmt, step.Query}
				fixture.RunStep(t, p.Ctx, referenceArithmeticExecutor{held, t}, backend.Recorder, step)
				return
			}
			fixture.RunStep(t, p.Ctx, referenceArithmeticExecutor{backend.DB, t}, backend.Recorder, step)
		})
	}
}

func referenceArithmeticRecordedClosure(t *testing.T, recorder *fixture.Recorder, query string, width, count int) {
	t.Helper()
	recorder.Mu.Lock()
	defer recorder.Mu.Unlock()
	for i := len(recorder.Operations) - 1; i >= 0; i-- {
		op := recorder.Operations[i]
		if op.SQL != query {
			continue
		}
		if op.Err != nil || !op.Closed || op.CloseErr != nil || len(op.Columns) != width || len(op.Rows) != count || len(op.Args) != 0 {
			t.Fatalf("recorded rows/closure for %s: %#v", query, op)
		}
		for _, row := range op.Rows {
			if len(row) != width {
				t.Fatalf("recorded row width for %s: %#v", query, row)
			}
		}
		return
	}
	t.Fatalf("missing recorded query: %s", query)
}
