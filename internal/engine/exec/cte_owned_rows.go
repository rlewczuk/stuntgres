package exec

// CTEOwnedRows owns the exact resources borrowed by its main streaming stage:
// the CTE invocations from Owner up to (excluding) Previous, and Source. Close
// closes the main stage first, then the CTE chain, then the captured sources.
// The scope's lexical CTE pointer is restored independently at the return boundary.
type CTEOwnedRows struct {
	RowResult
	Owner, Previous *CTEInvocation
	Source          *Source
	closed          bool
}

func (r *CTEOwnedRows) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	failure := r.RowResult.Close()
	for current := r.Owner; current != r.Previous; current = current.Parent {
		if err := current.Close(); failure == nil {
			failure = err
		}
		current.State.ReleaseMemberships()
	}
	if r.Owner != r.Previous {
		failure = r.Owner.State.FinishSources(failure)
	}
	r.Source.Close()
	r.Owner, r.Previous, r.Source = nil, nil, nil
	return failure
}
