package engine

import (
	"fmt"
	"path"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

const tablespaceLocationLimit = 970
const tablespacePhysicalUnsupported = "CREATE TABLESPACE physical locations are unsupported in embedded mode"

// The release reserves 54 bytes for the version directory, separators, OIDs
// and fork name. Validate canonical POSIX bytes without probing the host.
func tablespaceLocation(location string) (string, error) {
	if location != "" {
		location = path.Clean(location)
	}
	if strings.ContainsRune(location, '\'') {
		return location, sqlError("42602", "tablespace location cannot contain single quotes")
	}
	if !path.IsAbs(location) {
		return location, sqlError("42P17", "tablespace location must be an absolute path")
	}
	if len(location) > tablespaceLocationLimit {
		return location, sqlError("42P17", "tablespace location \""+location+"\" is too long")
	}
	return location, nil
}

// Names have already undergone lexer normalization and byte-safe truncation.
// The existing flag is a lookup result, not a tablespace publication facility.
func tablespaceName(name string, existing bool) error {
	if strings.HasPrefix(name, "pg_") {
		return roleDiagnostic("42939", "unacceptable tablespace name \""+name+"\"", "The prefix \"pg_\" is reserved for system tablespaces.")
	}
	if existing {
		return sqlError("42710", "tablespace \""+name+"\" already exists")
	}
	return nil
}

func (e *Engine) executeCreateTablespace(op *Operation, stmt *parser.CreateTablespace, count int) (Result, error) {
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
	if _, err := tablespaceLocation(stmt.Location); err != nil {
		return Result{}, err
	}
	if err := tablespaceName(stmt.Name.Text, stmt.Name.Text == "pg_default" || stmt.Name.Text == "pg_global"); err != nil {
		return Result{}, err
	}
	if err := op.Context().Err(); err != nil {
		return Result{}, err
	}
	// WITH is retained structurally but cannot be applied without physical
	// creation. Never allocate an OID, stage a row or pretend data was placed.
	return Result{}, sqlError("0A000", tablespacePhysicalUnsupported)
}
