package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

const tablespacePhysicalUnsupported = "CREATE TABLESPACE physical locations are unsupported in embedded mode"

func (e *Engine) executeCreateTablespace(op *session.Operation, stmt *parser.CreateTablespace, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return Result{}, err
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return Result{}, err
	}
	if !actor.Superuser {
		return Result{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied to create tablespace \"" + stmt.Name.Text + "\"", Hint: "Must be superuser to create a tablespace."}
	}
	owner := parser.RoleSpec{Identity: "current"}
	if stmt.Owner != nil {
		owner = *stmt.Owner
	}
	if _, err := authorization.MembershipRole(auth.Roles, auth.State, owner); err != nil {
		return Result{}, err
	}
	if _, err := catalog.ValidateTablespaceLocation(stmt.Location); err != nil {
		return Result{}, err
	}
	if err := catalog.ValidateTablespaceName(stmt.Name.Text, stmt.Name.Text == "pg_default" || stmt.Name.Text == "pg_global"); err != nil {
		return Result{}, err
	}
	if err := op.Context().Err(); err != nil {
		return Result{}, err
	}
	// WITH is retained structurally but cannot be applied without physical
	// creation. Never allocate an OID, stage a row or pretend data was placed.
	return Result{}, sqlError("0A000", tablespacePhysicalUnsupported)
}
