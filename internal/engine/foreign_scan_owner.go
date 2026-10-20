package engine

import "context"

// One statement or logical SQL cursor owns each captured foreign source. A
// finalizer can finish one completed entry early; Finish drains the remaining
// entries through this same authority, never through a second cleanup list.
type foreignScanOwner struct {
	sources    []*foreignQuerySource
	closeError error
}

func (owner *foreignScanOwner) add(source *foreignQuerySource) {
	source.lifetimeOwner = owner
	owner.sources = append(owner.sources, source)
}

func (owner *foreignScanOwner) finishSource(ctx context.Context, source *foreignQuerySource) error {
	if source.lifetimeClosed {
		return source.closeError
	}
	source.lifetimeClosed = true
	for _, execution := range source.executions {
		if err := execution.end(ctx); source.closeError == nil {
			source.closeError = err
		}
	}
	if owner.closeError == nil {
		owner.closeError = source.closeError
	}
	return source.closeError
}

func (owner *foreignScanOwner) finish(ctx context.Context) error {
	if owner == nil {
		return nil
	}
	for _, source := range owner.sources {
		owner.finishSource(ctx, source)
	}
	return owner.closeError
}

func (s *StatementScope) ownForeignSource(source *foreignQuerySource) {
	owner := &s.foreignTables().owner
	if s.foreignTablesOrZero().cursor != nil {
		owner = &s.foreignTables().cursor.foreignOwner
	}
	if *owner == nil {
		*owner = &foreignScanOwner{}
	}
	(*owner).add(source)
}

// The caller retains admission but holds no engine/transaction/session mutex.
// A primary operation failure is never replaced by cleanup failure.
func (s *StatementScope) finishForeignOwner(owner *foreignScanOwner, primary error) error {
	if owner == nil {
		return primary
	}
	if s.replacementRead {
		s.op.session.Engine().replacementMu.RUnlock()
		s.replacementRead = false
	}
	cleanup := owner.finish(s.op.Context())
	if primary != nil {
		return primary
	}
	return cleanup
}
