package engine

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

func materializedRefreshAccess(view access.View, env access.Environment, ref ids.ObjectRef) error {
	d, ok := view.Catalog.Relation(ref.Address)
	if !ok || ref.Domain() != view.Owner.Domain || ref.Epoch != view.Catalog.Epoch() {
		return ids.ErrCatalogReplaced
	}
	if d.RelationKind != catalog.TableRelation && d.RelationKind != catalog.PartitionedRelation && d.RelationKind != catalog.MaterializedViewRelation {
		return sqlError("42809", fmt.Sprintf(`"%s" is not a table or materialized view`, d.Name()))
	}
	if err := view.Check(env, access.Requirement{Object: ref, Privileges: authorization.MaintainPrivilege}); err != nil {
		return err
	}
	if d.RelationKind != catalog.MaterializedViewRelation {
		return sqlError("0A000", fmt.Sprintf(`"%s" is not a materialized view`, d.Name()))
	}
	return nil
}

// A preceding refresh changes rows/population/revision, not defining authority.
// Capture its new store only at target grant; never retry name resolution.
func materializedRefreshCompatible(old, current *catalog.Relation) bool {
	return current != nil && current.RelationKind == catalog.MaterializedViewRelation &&
		catalog.RelationDefiningAuthorityCompatible(old, current)
}

type materializedReplacement struct {
	original    *catalog.Relation
	predecessor *storage.Relation
	storage     *storage.Relation
	generation  uint64
	length      int
	descriptor  *catalog.Relation
	ends        *storage.DeletionRoot
	concurrent  bool
	indexCount  int
}

// materializedRefreshScope is the statement's REFRESH MATERIALIZED VIEW slot:
// the refresh flag, the captured source descriptors and their visibility data.
type materializedRefreshScope struct {
	refresh    bool
	sources    []catalog.Descriptor
	sourceView *visibility.Data
}
