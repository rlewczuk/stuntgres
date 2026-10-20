package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

// A binder borrows this capability only from its mutex-owning statement caller.
// The input resolver, type graph and CHECK evaluator are already captured; the
// unlocked phase never resolves a new name or retries a side-effecting CHECK.
func (s *StatementScope) DecodeLockedDomainInput(ctx context.Context, typ *sqltype.Descriptor, text string, typmod int32, input sqltype.InputEnvironment) (sqltype.Value, error) {
	eval, ok := input.DomainChecks.(*exec.Context)
	if !ok || scopeOf(eval.Host) != s || eval.Catalog.State() == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing captured domain input authority")
	}
	e := s.op.session.Engine()
	providers := typedef.CaptureDomainInputProviders(eval.Catalog, typ)
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
	if routineAuth := routineAuthorityView(eval); routineAuth != nil {
		current.State.CurrentUser = routineAuth.State.CurrentUser
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
	if current.Catalog.Owner() != eval.Catalog.Owner() || current.Catalog.Epoch() != eval.Catalog.Epoch() {
		return sqltype.Value{}, ids.ErrCatalogReplaced
	}
	for _, provider := range providers {
		if current.Catalog.Get(provider.Address()) != provider {
			return sqltype.Value{}, catalog.Conflict{}
		}
	}
	return value, nil
}

// domainScope is the statement's domain group: the lazily built domain check
// evaluation context, the prepared check programs and the input catalog.
// Every statement execution sets the input catalog, so it is a value group
// rather than a pointer slot.
type domainScope struct {
	evaluation     *exec.Context
	preparedChecks map[*catalog.Type]exec.DomainCheckProgram
	inputCatalog   catalog.Read
}

// endAdmission drops the prepared checks and the evaluation context borrowed
// from the statement's admission. The input catalog stays.
func (d *domainScope) endAdmission() {
	d.preparedChecks, d.evaluation = nil, nil
}
