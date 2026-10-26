package core

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
)

func (e *Engine) attachViewWriteSourcesLocked(scope *StatementScope, b *analyze.Binding) error {
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return err
	}
	auth.Catalog = e.PrivateCatalogLocked(scope.PendingCatalog())
	auth.Recapture(auth.Catalog)
	original := len(view.ViewWriteOf(b.ViewWrite).Logical.QuerySources)
	for i := original; i < len(b.QuerySources); i++ {
		rewritten := false
		for _, ordinal := range view.ViewWriteOf(b.ViewWrite).QueryOccurrences {
			if ordinal == i {
				rewritten = true
				break
			}
		}
		if !rewritten {
			continue
		}
		source := &b.QuerySources[i]
		source.Storage = e.PrivateStorageLocked(scope.PendingCatalog(), source.Ref.Address)
		for key, ordinal := range view.ViewWriteOf(b.ViewWrite).QueryOccurrences {
			if ordinal != i {
				continue
			}
			for _, layer := range view.ViewWriteOf(b.ViewWrite).Layers {
				if stored.ViewOf(layer.Relation) != key.Definition {
					continue
				}
				current := auth.Catalog.MustRelation(layer.Ref.Address)
				source.Principal = current.Owner
				if stored.ViewOf(current).Invoker() {
					source.Principal = auth.State.CurrentUser
				}
				break
			}
		}
		if len(scope.mutations) == 0 {
			scope.QuerySources = append(scope.QuerySources, *source)
		}
	}
	return nil
}
