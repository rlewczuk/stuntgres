package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
)

func (e *Engine) attachViewWriteSourcesLocked(scope *StatementScope, b *analyze.Binding) error {
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		return err
	}
	auth.Catalog = e.privateCatalogLocked(scope.owner.Catalog)
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
		source.Storage = e.privateStorageLocked(scope.owner.Catalog, source.Ref.Address)
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
			scope.querySources = append(scope.querySources, *source)
		}
	}
	return nil
}
