package core

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

// Called under the same capture lock as other runtime catalog facts. The
// producer retains only immutable typed values, never this runtime adapter.
func (r EngineCatalogRuntime) VisitNDistinct(ctx context.Context, visit func(ids.OID, bool, sqltype.Value) error) error {
	for _, raw := range r.Auth.Catalog.Descriptors() {
		relation, ok := raw.(*catalog.Relation)
		if !ok {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		metadata := session.ResolveRelationMetadata(r.Auth.PendingCatalog(), r.engine.Storage.RelationMetadata, relation.Address())
		facts := session.ResolveStatistics(r.Auth.PendingCatalog(), relation.Address(), metadata, relation, r.Auth.Catalog)
		if facts == nil {
			continue
		}
		for key, fact := range facts.Extended {
			if err := visit(key.Definition.Address.ObjectID, key.Inherited, fact.Value); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}
