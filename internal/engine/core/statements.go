package core

import "stuntgres/internal/parser"

// Statements is the family statement dispatch that executeStatement hands
// over to. The composition sets Engine.Statements; item 195 moves the
// interface to core. Both methods report handled=false, having run nothing,
// for a statement that is not theirs, and executeStatement continues.
type Statements interface {
	// Execute runs a family statement with nothing held.
	Execute(e *Engine, scope *StatementScope, stmt parser.Statement, count int) (result Result, handled bool, err error)
	// ExecuteLocked is a lock-domain continuation: it is entered with
	// Engine.mu held and, when handled, has released it on every return.
	// When not handled, Engine.mu is still held.
	ExecuteLocked(e *Engine, scope *StatementScope, auth AuthorizationView, stmt parser.Statement, count int) (result Result, handled bool, err error)
}
