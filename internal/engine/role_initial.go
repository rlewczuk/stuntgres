package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/settings"
)

// The role default is an internal physical-session hook, not a registered GUC
// or a public connector option. Providers run without publication locks.
func (e *Engine) initialRole(ctx context.Context, authenticated authorization.PrincipalRef, provider settings.SessionDefaultProvider) (authorization.PrincipalRef, error) {
	if provider == nil {
		return authorization.PrincipalRef{}, nil
	}
	name, present, err := provider.SessionDefault(ctx, settings.Definition{Name: "role", BootDefault: "none", Type: settings.String})
	if err != nil {
		return authorization.PrincipalRef{}, err
	}
	if err := ctx.Err(); err != nil {
		return authorization.PrincipalRef{}, err
	}
	if !present || name == "none" {
		return authorization.PrincipalRef{}, nil
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	roles := e.RoleStore.PublishedRead(&e.Catalog.Owner, e.Catalog.Current())
	target, ok := roles.Lookup(name)
	if !ok {
		return authorization.PrincipalRef{}, SQLError("22023", fmt.Sprintf("role \"%s\" does not exist", name))
	}
	if err := authorization.ValidateInitialRole(roles, authenticated, target.Ref); err != nil {
		return authorization.PrincipalRef{}, err
	}
	return target.Ref, nil
}
