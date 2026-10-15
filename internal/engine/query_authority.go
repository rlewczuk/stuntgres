package engine

import (
	"context"
	"time"
)

// queryAuthority is a borrowed, statement-admission-only slot. Captured query
// trees share it; clearing it never changes their immutable data or frames.
// Session transaction admission serializes internal access. This slot must not
// implement context.Context: callbacks may retain a context after admission ends.
type queryAuthority struct {
	scope      *StatementScope
	expression *cursorExpressionData
}
type cursorExpressionData struct {
	identity       identitySnapshot
	statementStart time.Time
}

func (s *querySource) liveScope() *StatementScope {
	if s == nil {
		return nil
	}
	if s.authority != nil {
		return s.authority.scope
	}
	return s.scope // Standalone internal query fixtures have no admission slot.
}
func (s *querySource) borrowAuthority(scope *StatementScope) {
	s.authority = scope.authority
	s.scope = nil
	if s.authority == nil {
		s.scope = scope
	}
}
func (c *EvalContext) liveScope() *StatementScope {
	if c == nil {
		return nil
	}
	if c.query != nil {
		return c.query.source.liveScope()
	}
	return c.statementScope
}
func (c *EvalContext) setContext(ctx context.Context) {
	if c.query != nil && c.query.executionContext != nil {
		c.Context = c.query.executionContext
	} else if ctx == nil && c.query != nil && c.query.source != nil && c.query.source.authority != nil && c.query.source.authority.scope != nil {
		// Initial evaluators may be constructed without a pull context. Capture
		// the admitted request itself, never the mutable authority that owns it.
		c.Context = c.query.source.authority.scope.op.Context()
	} else {
		c.Context = ctx
	}
}
