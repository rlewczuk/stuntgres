package core

import (
	"fmt"

	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
)

func restrictedParameter(op *session.Operation, name string) error {
	name = settings.FoldName(name)
	if name == "role" || name == "session_authorization" {
		if op.Restricted != nil {
			return SQLError("42501", fmt.Sprintf("cannot set parameter %q within security-restricted operation", name))
		}
		if op.EffectiveUser != nil {
			return SQLError("42501", fmt.Sprintf("cannot set parameter %q within security-definer function", name))
		}
	}
	return nil
}
func restrictedStatement(op *session.Operation, statement parser.Statement) error {
	if op.Restricted == nil {
		switch s := statement.(type) {
		case *parser.RoleSelection:
			return restrictedParameter(op, "role")
		case *parser.SessionAuthorization:
			return restrictedParameter(op, "session_authorization")
		case *parser.Set:
			return restrictedParameter(op, s.Name.Text)
		case *parser.Reset:
			if !s.All {
				return restrictedParameter(op, s.Name.Text)
			}
		}
		return nil
	}
	tag := ""
	switch s := statement.(type) {
	case *parser.CloseCursor:
		tag = "CLOSE"
	case *parser.PrepareSQL:
		tag = "PREPARE"
	case *parser.DeallocateSQL:
		tag = "DEALLOCATE"
	case *parser.Listen:
		tag = "LISTEN"
	case *parser.Unlisten:
		tag = "UNLISTEN"
	case *parser.Discard:
		tag = "DISCARD"
	case *parser.RoleSelection:
		return restrictedParameter(op, "role")
	case *parser.SessionAuthorization:
		return restrictedParameter(op, "session_authorization")
	case *parser.Set:
		return restrictedParameter(op, s.Name.Text)
	case *parser.Reset:
		if !s.All {
			return restrictedParameter(op, s.Name.Text)
		}
	case *parser.CreateTable:
		if s.Persistence == "temporary" {
			return SQLError("42501", "cannot create temporary table within security-restricted operation")
		}
	case *parser.DeclareCursor:
		if s.Hold {
			return SQLError("42501", "cannot create a cursor WITH HOLD within security-restricted operation")
		}
	}
	if tag != "" {
		return SQLError("42501", fmt.Sprintf("cannot execute %s within security-restricted operation", tag))
	}
	return nil
}
