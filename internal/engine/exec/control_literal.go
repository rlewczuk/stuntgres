package exec

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// ControlLiteralAuthority identifies the private canonical statistics codec,
// not a client subject or general hostless/planning exemption. Its captured
// immutable root and bootstrap actor cannot be replaced by an OID-shaped value.
// It is invocation-local and is never serialized or retained by a recipe.
type ControlLiteralAuthority struct {
	read  catalog.Read
	actor authorization.PrincipalRef
}

// NewControlLiteralAuthority is minted only by the genuine captured statistics
// codec host, after selecting its existing bootstrap constructor authority.
func NewControlLiteralAuthority(auth access.View) ControlLiteralAuthority {
	if auth.Owner == nil || auth.Owner.Principals == nil || auth.Sepgsql.Policy != nil || auth.State.CurrentUser != auth.Owner.Principals.Principal(authorization.BootstrapPrincipalID) {
		return ControlLiteralAuthority{}
	}
	return ControlLiteralAuthority{read: auth.Catalog, actor: auth.State.CurrentUser}
}
func (a ControlLiteralAuthority) present() bool { return a.read.Owner() != nil || a.actor.ID() != 0 }

// controlLiteralAccess is the single typed-origin check shared by actual
// constructor, text-codec and module-input lifecycle owners. A live client
// evaluator cannot borrow an otherwise same-root bootstrap codec capture.
func (c *Context) controlLiteralAccess() (RoutineAccess, []catalog.ExpressionDependency, error) {
	auth, dependencies, err := RoutineAdmissionAuthority(c)
	if err != nil {
		return auth, nil, err
	}
	origin := c.ControlLiteralAuthority
	if origin.read.Owner() == nil || auth.Owner != origin.read.Owner() || auth.Owner.Principals == nil || auth.Sepgsql.Policy != nil || c.LiveHost() != nil || origin.actor != auth.Owner.Principals.Principal(authorization.BootstrapPrincipalID) || auth.State.CurrentUser != origin.actor || c.Catalog.Owner() != origin.read.Owner() || c.Catalog.Domain() != origin.read.Domain() || c.Catalog.Epoch() != origin.read.Epoch() || c.Catalog.State() != origin.read.State() || auth.Catalog.State() != origin.read.State() || auth.Catalog.Domain() != origin.read.Domain() || auth.Catalog.Epoch() != origin.read.Epoch() {
		return auth, nil, fmt.Errorf("stuntgres: invalid canonical control literal authority")
	}
	if _, err = auth.Roles.Descriptor(origin.actor); err != nil {
		return auth, nil, err
	}
	return auth, dependencies, nil
}

func (c *Context) enterControlLiteral(reference *catalog.Procedure, recipe expr.RoutineRecipeReference) (sqltype.RoutineBodyToken, error) {
	auth, dependencies, err := c.controlLiteralAccess()
	if err != nil {
		return sqltype.RoutineBodyToken{}, err
	}
	selected := c
	var local Context
	if recipe != nil {
		local = *c
		selected = &local
		if err = SelectExpressionRecipe(selected, recipe); err != nil {
			return sqltype.RoutineBodyToken{}, err
		}
	}
	// Existing syntax whitelist, binder capture and canonical typed reencoding own
	// admissibility. This still checks original recipe/declaration lifetime and
	// bootstrap SQL ACL; only physical-session MAC/FHET is inapplicable here.
	auth.Sepgsql = access.SepgsqlAuthority{}
	if err = CheckRecipeRoutineDescriptor(selected, auth, dependencies, reference); err != nil {
		return sqltype.RoutineBodyToken{}, err
	}
	return sqltype.RoutineBodyToken{}, nil
}
