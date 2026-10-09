package reference_test

import (
	"context"
	"database/sql"
	"testing"
)

// Arithmetic writes must not publish a result when analysis or execution fails.
// Keep this public-interface assertion beside the migrated consumer scenarios.
type referenceArithmeticExecutor struct {
	referenceExecutor
	t *testing.T
}

func (e referenceArithmeticExecutor) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	result, err := e.referenceExecutor.ExecContext(ctx, query, args...)
	if err != nil && result != nil {
		e.t.Fatalf("failed Exec exposed a result: %s: %v", query, err)
	}
	return result, err
}

func referenceArithmeticFailure(t *testing.T, p *referencePair, step referenceStep) {
	t.Helper()
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			step := step
			if backend.pg && step.postgresDiagnostic != nil {
				step.state = step.postgresDiagnostic.Code
			}
			if step.prepared && !step.selectRows && step.errorStage == "execute" {
				stmt, err := backend.db.PrepareContext(p.ctx, step.query)
				if err != nil {
					t.Fatal("execution failure moved into Prepare", err)
				}
				defer func() {
					if err := stmt.Close(); err != nil {
						t.Error(err)
					}
				}()
				step.prepared = false
				held := referenceHeldStatement{backend.db, stmt, step.query}
				referenceRunStep(t, p.ctx, referenceArithmeticExecutor{held, t}, backend.recorder, step)
				return
			}
			referenceRunStep(t, p.ctx, referenceArithmeticExecutor{backend.db, t}, backend.recorder, step)
		})
	}
}

func referenceArithmeticRecordedClosure(t *testing.T, recorder *referenceRecorder, query string, width, count int) {
	t.Helper()
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	for i := len(recorder.operations) - 1; i >= 0; i-- {
		op := recorder.operations[i]
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
