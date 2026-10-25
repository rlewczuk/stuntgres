package tablecmds

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func acquireCatalogBinding(s *core.StatementScope, base catalog.Read, claim catalog.BindingClaim) error {
	key := claim.Key
	resource := lock.Resource{ObjectRef: s.Engine().Catalog.Domain.Ref(base.Epoch(), key.Namespace), Name: key}
	if err := s.AcquireCatalogResource(resource, lock.AccessExclusive); err != nil {
		return err
	}
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(s.Op)
	if err != nil {
		return err
	}
	if auth.Catalog.Epoch() != base.Epoch() {
		return ids.ErrCatalogReplaced
	}
	return catalog.BindingCurrent(base, auth.Catalog, claim)
}
