package core

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

// The role hook is separate from ordinary GUCs. Resolution uses the coherent
// pre-callback role capture; final publication still fences live eligibility.
func (e *Engine) initialRole(op *session.Operation, authenticated authorization.PrincipalRef, provider settings.SessionDefaultProvider, roles authorization.Read, persisted session.CapturedDefault) (authorization.PrincipalRef, string, error) {
	name, supplied := persisted.Input, false
	if provider != nil {
		input, present, err := provider.SessionDefault(op.Context(), settings.Definition{Name: "role", BootDefault: "none", Type: settings.String})
		if err != nil {
			return authorization.PrincipalRef{}, "", err
		}
		if present {
			name, supplied = input, true
		}
	}
	if name == "none" || (!supplied && persisted.Source == session.DefaultSourceDefault) {
		return authorization.PrincipalRef{}, "", nil
	}
	if err := op.Context().Err(); err != nil {
		return authorization.PrincipalRef{}, "", err
	}
	name, notice, err := settings.NormalizeCatalogName(name)
	if err != nil {
		return authorization.PrincipalRef{}, "", err
	}
	if notice != nil {
		op.EmitNotice(session.LevelNotice, *notice)
	}
	target, exists := roles.Lookup(name)
	if !exists {
		err = SQLError("22023", fmt.Sprintf(`role "%s" does not exist`, name))
	} else {
		err = authorization.ValidateInitialRole(roles, authenticated, target.Ref)
	}
	if err != nil {
		// Native provider failures retain their established fatal semantics. Invalid
		// persisted text instead warns and leaves the authenticated identity intact.
		if supplied {
			return authorization.PrincipalRef{}, "", err
		}
		code, message := "42501", fmt.Sprintf(`permission denied to set role "%s"`, name)
		if !exists {
			code, message = "22023", fmt.Sprintf(`role "%s" does not exist`, name)
		}
		op.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: code, Message: message})
		return authorization.PrincipalRef{}, "", nil
	}
	return target.Ref, name, nil
}
