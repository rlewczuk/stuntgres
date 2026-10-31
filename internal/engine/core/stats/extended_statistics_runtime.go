package stats

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// VisitExtended projects each extended statistics fact. The catalog runtime
// calls it under the same capture lock as other runtime catalog facts, with
// the engine's store. The producer retains only immutable typed values, never
// this runtime adapter.
func VisitExtended[A CatalogAuthority](ctx context.Context, auth A, store *storage.Store, visit func(ids.OID, bool, sqltype.Value, sqltype.Value, sqltype.Value, sqltype.Value) error) error {
	read := auth.Access().Catalog
	for _, raw := range read.Descriptors() {
		relation, ok := raw.(*catalog.Relation)
		if !ok {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		metadata := session.ResolveRelationMetadata(auth.PendingCatalog(), store.RelationMetadata, relation.Address())
		facts := session.ResolveStatistics(auth.PendingCatalog(), relation.Address(), metadata, relation, read)
		if facts == nil {
			continue
		}
		for key, fact := range facts.Extended {
			joint := fact.Value
			if joint.Type() == nil {
				d, _ := read.LookupType(sqltype.PgNDistinct)
				joint = sqltype.ResultNull(d.Usage())
			}
			dependencies := fact.Dependencies
			if dependencies.Type() == nil {
				d, _ := read.LookupType(sqltype.PgDependencies)
				dependencies = sqltype.ResultNull(d.Usage())
			}
			mcv := fact.MCV
			if mcv.Type() == nil {
				d, _ := read.LookupType(sqltype.PgMCVList)
				mcv = sqltype.ResultNull(d.Usage())
			}
			expressions, err := statisticExpressionRows(ctx, read, fact)
			if err != nil {
				return err
			}
			if err := visit(key.Definition.Address.ObjectID, key.Inherited, joint, dependencies, mcv, expressions); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}
