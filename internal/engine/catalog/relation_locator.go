package catalog

import "stuntgres/internal/engine/ids"

// EffectiveStorageLocator identifies a native logical storage incarnation. It
// does not name a file. Provider, partitioned and virtual relations have none.
func (r *Relation) EffectiveStorageLocator(view Read) ids.OID {
	if r.VirtualView || r.CatalogView || r.Partition != nil || r.Foreign != nil {
		return 0
	}
	if r.RelationKind != TableRelation && r.RelationKind != MaterializedViewRelation && !(r.RelationKind == SequenceRelation && r.Sequence != nil) && r.SQLIndex == nil {
		return 0
	}
	if r.SQLIndex != nil {
		if parent, ok := view.Relation(r.SQLIndex.Parent); ok && parent.Partition != nil {
			return 0
		}
	}
	if r.StorageLocator != 0 {
		return r.StorageLocator
	}
	return r.Address().ObjectID
}
