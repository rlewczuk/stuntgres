package engine

import "stuntgres/internal/engine/exec"

// cteOwnedRows owns the exact resources borrowed by its main streaming stage.
// The scope's lexical CTE pointer is restored independently at the return boundary.
type cteOwnedRows struct {
	exec.RowResult
	owner, previous *exec.CTEInvocation
	source          *exec.Source
	closed          bool
}

func (r *cteOwnedRows) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	failure := r.RowResult.Close()
	for current := r.owner; current != r.previous; current = current.Parent {
		if err := current.Close(); failure == nil {
			failure = err
		}
		current.State.ReleaseMemberships()
	}
	if r.owner != r.previous {
		failure = r.owner.State.FinishSources(failure)
	}
	r.source.Close()
	r.owner, r.previous, r.source = nil, nil, nil
	return failure
}
