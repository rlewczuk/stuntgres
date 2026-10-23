package fdw

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// UpdatableEvents inspects actual registered capabilities without planning,
// opening a scan, checking row identity or calling application code.
func UpdatableEvents(ctx context.Context, view catalog.Read, relation *catalog.Relation) (bool, bool, error) {
	if err := ctx.Err(); err != nil {
		return false, false, err
	}
	if relation == nil || view.Get(relation.Address()) != relation || relation.Foreign == nil {
		return false, false, ids.ErrCatalogReplaced
	}
	server, ok := view.Foreign(relation.Foreign.Server)
	if !ok {
		return false, false, ids.ErrCatalogReplaced
	}
	wrapper, ok := view.Foreign(server.Wrapper)
	if !ok {
		return false, false, ids.ErrCatalogReplaced
	}
	handler, err := HandlerFor(view, wrapper)
	if err != nil {
		return false, false, err
	}
	_, update := handler.Wrapper.(catalog.ForeignUpdater)
	_, remove := handler.Wrapper.(catalog.ForeignDeleter)
	return update, remove, nil
}
