package fdw

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/settings"
)

// UpdatableEvents inspects actual registered capabilities without planning,
// opening a scan, checking row identity or calling application code.
func UpdatableEvents(ctx context.Context, view catalog.Read, relation *catalog.Relation, restricted settings.RestrictedRelationKinds) (bool, bool, error) {
	if err := ctx.Err(); err != nil {
		return false, false, err
	}
	if relation == nil || view.Get(relation.Address()) != relation || relation.Foreign == nil {
		return false, false, ids.ErrCatalogReplaced
	}
	handler, err := RelationHandler(view, relation, restricted)
	if err != nil {
		return false, false, err
	}
	_, update := handler.Wrapper.(catalog.ForeignUpdater)
	_, remove := handler.Wrapper.(catalog.ForeignDeleter)
	return update, remove, nil
}
