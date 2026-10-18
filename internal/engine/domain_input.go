package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// A binder borrows this capability only from its mutex-owning statement caller.
// The input resolver, type graph and CHECK evaluator are already captured; the
// unlocked phase never resolves a new name or retries a side-effecting CHECK.
func (s *StatementScope) DecodeLockedDomainInput(ctx context.Context, typ *sqltype.Descriptor, text string, typmod int32, input sqltype.InputEnvironment) (sqltype.Value, error) {
	eval, ok := input.DomainChecks.(*EvalContext)
	if !ok || eval.statementScope != s || eval.catalog.state == nil {
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
		current.state.CurrentUser = eval.routineAuthority.state.CurrentUser
		if err := current.roles.ValidatePrincipal(current.state.CurrentUser); err != nil {
			return sqltype.Value{}, err
		}
	}
	e.mu.Unlock()
	err = s.readmitPreparedPrograms(current)
	e.mu.Lock()
	if err != nil {
		return sqltype.Value{}, err
	}
	if current.catalog.owner != eval.catalog.owner || current.catalog.Epoch() != eval.catalog.Epoch() {
		return sqltype.Value{}, ids.ErrCatalogReplaced
	}
	for _, provider := range providers {
		if current.catalog.Get(provider.Address()) != provider {
			return sqltype.Value{}, CatalogConflict{}
		}
	}
	return value, nil
}

// Input order is lexical (parameter/member order), not a later value-tree walk.
// This capture concerns only providers of the demanded input recipe, before
// leaving the binder's lock, and is discarded with that synchronous occurrence.
func captureDomainInputProviders(view CatalogRead, typ *sqltype.Descriptor) map[ids.ObjectAddress]Descriptor {
	providers := make(map[ids.ObjectAddress]Descriptor)
	var visit func(*sqltype.Descriptor)
	visit = func(typ *sqltype.Descriptor) {
		address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())}
		if _, visited := providers[address]; visited {
			return
		}
		if descriptor, exists := view.Descriptor(address); exists {
			providers[address] = descriptor
			if domain, ok := descriptor.(*typeDescriptor); ok {
				for _, check := range domain.domainChecks {
					for _, dependency := range check.dependencies {
						address := dependency.ref.Address
						address.SubID = 0
						if provider, exists := view.Descriptor(address); exists {
							if target, typed := provider.(*typeDescriptor); typed {
								visit(target.typ)
							} else {
								providers[address] = provider
							}
						}
					}
				}
			}
		}
		if typ.IsDomain() {
			visit(typ.DomainBase().Type)
		}
		if typ.BaseType().IsArray() {
			visit(typ.BaseType().Element())
		}
		if typ.BaseType().IsComposite() {
			for i := range typ.BaseType().CompositeLen() {
				visit(typ.BaseType().CompositeMember(i).Usage.Type)
			}
		}
	}
	visit(typ)
	return providers
}

func validateCapturedInputBinding(captured, current CatalogRead, dependencies []expressionDependency) error {
	if captured.owner != current.owner || captured.Epoch() != current.Epoch() {
		return ids.ErrCatalogReplaced
	}
	for _, dependency := range dependencies {
		address := dependency.ref.Address
		address.SubID = 0
		provider, exists := captured.Descriptor(address)
		if !exists {
			continue
		} // Anonymous expression shapes are not catalog objects.
		if current.Get(address) != provider {
			return CatalogConflict{}
		}
	}
	return nil
}

func (b *expressionBinder) decodeLiteralInput(text string, typ *sqltype.Descriptor, typmod int32, input sqltype.InputEnvironment) (sqltype.Value, error) {
	if !sqltype.ContainsDomainChecks(typ) {
		return typ.InputWithEnvironment(b.ctx, text, typmod, input)
	}
	owner, ok := input.LockedDomainInput.(*StatementScope)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing locked domain literal input boundary")
	}
	var auth AuthorizationView
	if b.queryAuth != nil {
		auth = *b.queryAuth
	} else {
		var err error
		auth, err = b.engine.authorizationLocked(b.op)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	if owner.routineAuth.engine != nil && owner.foreignActionAuth == nil {
		auth.state.CurrentUser = owner.routineAuth.state.CurrentUser
		if err := auth.roles.ValidatePrincipal(auth.state.CurrentUser); err != nil {
			return sqltype.Value{}, err
		}
	} else if owner.routineAuth.engine == nil {
		owner.routineAuth = auth
	}
	eval := EvalContext{Context: b.ctx, statementScope: owner, catalog: b.bindingCatalog(), Input: input, Output: owner.output, routineAuthority: &auth}
	input.DomainChecks = &eval
	return owner.DecodeLockedDomainInput(b.ctx, typ, text, typmod, input)
}
