package schemacmds

import (
 "fmt"
 "stuntgres/internal/engine/core"
 "stuntgres/internal/engine/ddl"
 "stuntgres/internal/engine/session"
 "stuntgres/internal/parser"
)

// ExecuteSecurityLabel runs after ordinary session, failed-transaction, read-only
// and eligible command-start event admission. Provider resolution precedes target
// lookup/ownership. With no installed providers there is no target/mutation phase.
func ExecuteSecurityLabel(e *core.Engine, op *session.Operation, stmt *parser.SecurityLabel, count int) (core.Result, error) {
 if count != 0 { return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count) }
 e.Mu.Lock()
 defer e.Mu.Unlock()
 auth, err := e.AuthorizationLocked(op)
 if err != nil { return core.Result{}, err }
 return core.Result{}, ddl.SecurityLabel(op.Context(), auth.View, stmt)
}
