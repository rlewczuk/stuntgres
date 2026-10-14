package engine

import (
	"context"
	"time"
)

// queryAuthority is a borrowed, statement-admission-only slot. Captured query
// trees share it; clearing it never changes their immutable data or frames.
// Session transaction admission serializes every access.
type queryAuthority struct {
	scope      *StatementScope
	expression *cursorExpressionData
}
type cursorExpressionData struct {
	identity       identitySnapshot
	statementStart time.Time
}

var endedQueryAuthority = func() <-chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}()

func (a *queryAuthority) context() context.Context {
	if a == nil || a.scope == nil {
		return nil
	}
	return a.scope.op.Context()
}
func (a *queryAuthority) Deadline() (time.Time, bool) {
	if c := a.context(); c != nil {
		return c.Deadline()
	}
	return time.Time{}, false
}
func (a *queryAuthority) Done() <-chan struct{} {
	if c := a.context(); c != nil {
		return c.Done()
	}
	return endedQueryAuthority
}
func (a *queryAuthority) Err() error {
	if c := a.context(); c != nil {
		return c.Err()
	}
	return context.Canceled
}
func (a *queryAuthority) Value(key any) any {
	if c := a.context(); c != nil {
		return c.Value(key)
	}
	return nil
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
	} else if c.query != nil && c.query.source != nil && c.query.source.authority != nil {
		c.Context = c.query.source.authority
	} else {
		c.Context = ctx
	}
}
