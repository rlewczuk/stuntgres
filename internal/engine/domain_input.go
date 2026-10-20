package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// A binder borrows this capability only from its mutex-owning statement caller.
// The input resolver, type graph and CHECK evaluator are already captured; the
// unlocked phase never resolves a new name or retries a side-effecting CHECK.
func (s *StatementScope) DecodeLockedDomainInput(ctx context.Context, typ *sqltype.Descriptor, text string, typmod int32, input sqltype.InputEnvironment) (sqltype.Value, error) {
	eval, ok := input.DomainChecks.(*EvalContext)
	if !ok || eval.statementScope != s || eval.catalog.State() == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing captured domain input authority")
	}
	e := s.op.session.Engine()
	providers := captureDomainInputProviders(eval.catalog, typ)
	e.mu.Unlock()
	value, err := typ.InputWithEnvironment(ctx, text, typmod, input)
	e.mu.Lock()
	if err != nil {
		return sqltype.Value{}, err
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	current, err := e.authorizationLocked(s.op)
	if err != nil {
		return sqltype.Value{}, err
	}
	// Preserve the actual trusted caller captured before input, including a
	// local RI actor; later session capture cannot substitute another principal.
	if eval.routineAuthority != nil {
		current.State.CurrentUser = eval.routineAuthority.State.CurrentUser
		if err := current.Roles.ValidatePrincipal(current.State.CurrentUser); err != nil {
			return sqltype.Value{}, err
		}
	}
	e.mu.Unlock()
	err = s.readmitPreparedPrograms(current)
	e.mu.Lock()
	if err != nil {
		return sqltype.Value{}, err
	}
	if current.Catalog.Owner() != eval.catalog.Owner() || current.Catalog.Epoch() != eval.catalog.Epoch() {
		return sqltype.Value{}, ids.ErrCatalogReplaced
	}
	for _, provider := range providers {
		if current.Catalog.Get(provider.Address()) != provider {
			return sqltype.Value{}, catalog.Conflict{}
		}
	}
	return value, nil
}

func (b *expressionBinder) decodeLiteralInput(text string, typ *sqltype.Descriptor, typmod int32, input sqltype.InputEnvironment) (sqltype.Value, error) {
	if !sqltype.ContainsDomainConstraints(typ) {
		return typ.InputWithEnvironment(b.ctx, text, typmod, input)
	}
	owner, ok := input.LockedDomainInput.(*StatementScope)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing locked domain literal input boundary")
	}
	var auth AuthorizationView
	if b.query.auth != nil {
		auth = *b.query.auth
	} else {
		var err error
		auth, err = b.engine.authorizationLocked(b.op)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	if owner.routineAuth.engine != nil && owner.foreignActionAuth == nil {
		auth.State.CurrentUser = owner.routineAuth.State.CurrentUser
		if err := auth.Roles.ValidatePrincipal(auth.State.CurrentUser); err != nil {
			return sqltype.Value{}, err
		}
	} else if owner.routineAuth.engine == nil {
		owner.routineAuth = auth
	}
	eval := EvalContext{evalStatement: evalStatement{Context: b.ctx, statementScope: owner, catalog: b.bindingCatalog(), Input: input, Output: owner.output}, evalAdmission: evalAdmission{routineAuthority: &auth}}
	input.DomainChecks = &eval
	return owner.DecodeLockedDomainInput(b.ctx, typ, text, typmod, input)
}
