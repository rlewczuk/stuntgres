package exec

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqltype"
)

type IdentitySnapshot struct {
	Database, Current, Session, System sqltype.Value
	CurrentMissing, SessionMissing     authorization.PrincipalRef
	CurrentPrincipal                   authorization.PrincipalRef
}

// CaptureIdentity captures the seven selected identities under the binding
// catalog/authorization authority. Their leaves retain no session and perform
// no live role lookup.
func CaptureIdentity(read catalog.Read, roles authorization.Read, state authorization.RoleState, database string) (IdentitySnapshot, error) {
	name, _ := read.LookupType(sqltype.Name)
	text, _ := read.LookupType(sqltype.Text)
	if err := roles.ValidatePrincipal(state.CurrentUser); err != nil {
		return IdentitySnapshot{}, err
	}
	current, currentExists := roles.Find(state.CurrentUser)
	if err := roles.ValidatePrincipal(state.SessionUser); err != nil {
		return IdentitySnapshot{}, err
	}
	session, sessionExists := roles.Find(state.SessionUser)
	identity := IdentitySnapshot{Database: sqltype.String(name, database), System: sqltype.Null(text), CurrentPrincipal: state.CurrentUser}
	if sessionExists {
		identity.Session = sqltype.String(name, session.Name)
	} else {
		identity.SessionMissing = state.SessionUser
	}
	if currentExists {
		identity.Current = sqltype.String(name, current.Name)
	} else {
		identity.CurrentMissing = state.CurrentUser
	}
	return identity, nil
}

// Value returns the captured identity of kind. The current schema is not
// captured: the engine reads it from the statement's search path.
func (i IdentitySnapshot) Value(kind functions.IdentityKind) (sqltype.Value, error) {
	switch kind {
	case functions.IdentityCatalog, functions.IdentityDatabase:
		return i.Database, nil
	case functions.IdentityRole, functions.IdentityCurrentUser, functions.IdentityUser:
		if i.CurrentMissing != (authorization.PrincipalRef{}) {
			return sqltype.Value{}, sqlError("42704", fmt.Sprintf("invalid role OID: %d", i.CurrentMissing.ID()))
		}
		return i.Current, nil
	case functions.IdentitySessionUser:
		if i.SessionMissing != (authorization.PrincipalRef{}) {
			return sqltype.Value{}, sqlError("42704", fmt.Sprintf("invalid role OID: %d", i.SessionMissing.ID()))
		}
		return i.Session, nil
	case functions.IdentitySystemUser:
		return i.System, nil
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: invalid identity expression")
}
