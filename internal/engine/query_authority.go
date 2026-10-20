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
	host       execHost
	expression *cursorExpressionData
}
type cursorExpressionData struct {
	identity       identitySnapshot
	statementStart time.Time
}

func (s *querySource) borrowAuthority(scope *StatementScope) {
	s.authority = scope.authority
	s.host = nil
	if s.authority == nil {
		s.host = hostOf(scope)
	}
}

// liveHost is the runtime's only route to statement, session and engine
// state. It returns an untyped nil when no statement scope is attached.
func (c *EvalContext) liveHost() execHost {
	if c == nil {
		return nil
	}
	if c.query != nil {
		return c.query.source.liveHost()
	}
	return c.host
}
func (s *querySource) liveHost() execHost {
	if s == nil {
		return nil
	}
	if s.authority != nil {
		return s.authority.host
	}
	return s.host // Standalone internal query fixtures have no admission slot.
}

// presentation formats reg* and aclitem output from the statement snapshot,
// or reports the missing snapshot when the record is detached.
func (c *EvalContext) presentation() presentationFormatters {
	if host := c.liveHost(); host != nil {
		return host
	}
	return detachedPresentation{}
}
func (c *EvalContext) setContext(ctx context.Context) {
	if c.query != nil && c.query.executionContext != nil {
		c.Context = c.query.executionContext
	} else if ctx == nil && c.query != nil && c.query.source != nil && c.query.source.authority != nil && c.query.source.authority.host != nil {
		// Initial evaluators may be constructed without a pull context. Capture
		// the admitted request itself, never the mutable authority that owns it.
		c.Context = c.query.source.authority.host.Context()
	} else {
		c.Context = ctx
	}
}
