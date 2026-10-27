package exec

import (
	"context"
	"errors"
	"stuntgres/internal/sqltype"
)

// CTEOwnedRows owns the exact resources borrowed by its main streaming stage:
// the CTE invocations from Owner up to (excluding) Previous, and Source. Close
// closes the main stage, completes remaining modifiers only after successful
// demand/close, then closes the CTE chain and captured sources.
// The scope's lexical CTE pointer is restored independently at the return boundary.
type CTEOwnedRows struct {
	RowResult
	Owner, Previous *CTEInvocation
	Source          *Source
	Complete        func() error // Exhaust auxiliary modifiers after main demand.
	failure         error
	closed          bool
}

func (r *CTEOwnedRows) Next(ctx context.Context, row []sqltype.Value) (bool, error) {
	present, err := r.RowResult.Next(ctx, row)
	if r.failure == nil {
		r.failure = err
	}
	return present, err
}

func (r *CTEOwnedRows) Close() error {
	execution, cleanup := r.Finish(nil)
	if execution == nil {
		return cleanup
	}
	if cleanup == nil {
		return execution
	}
	return errors.Join(execution, cleanup)
}

// Finish ends actual command execution before releasing its borrowed resources.
// A caller failure aborts pending auxiliaries. Execution errors remain catchable
// SQL failures; resource-close errors are separate fatal cleanup evidence for PL.
func (r *CTEOwnedRows) Finish(primary error) (execution, cleanup error) {
	if r.closed {
		return nil, nil
	}
	r.closed = true
	cleanup = r.RowResult.Close()
	if primary == nil && cleanup == nil && r.failure == nil && r.Complete != nil {
		execution = r.Complete()
	}
	r.Complete = nil
	for current := r.Owner; current != r.Previous; current = current.Parent {
		if err := current.CloseResources(); err != nil {
			if cleanup == nil {
				cleanup = err
			} else {
				cleanup = errors.Join(cleanup, err)
			}
		}
		current.State.ReleaseMemberships()
	}
	if r.Owner != r.Previous {
		cause := primary
		if cause == nil {
			cause = r.failure
		}
		if cause == nil {
			cause = execution
		}
		if cause != nil {
			// Source failure aborts reached borrowed producers, including a
			// failure outside Next. It must not be recast as a close failure.
			_ = r.Owner.State.FinishSources(cause)
		} else {
			cleanup = r.Owner.State.FinishSources(cleanup)
		}
	}
	r.Source.Close()
	r.Owner, r.Previous, r.Source = nil, nil, nil
	r.failure = nil
	return execution, cleanup
}
