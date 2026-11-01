package view

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
)

// SameStableReadLineage selects an owner's actual old roots for a new query
// admitted through current SQL name, schema and ACL authority. Name, owner and
// equivalent layout backing may change; actual view-definition, row-type,
// table-method and row-shape identity, grounded family and boundary must match.
// This is not catalog-validity checking; callers own object domain/epoch checks.
func SameStableReadLineage(old, current *catalog.Relation, a, b *storage.Relation) bool {
	return sameRetainedFamily(old, current, a, b) && old.View == current.View && old.RowType == current.RowType && old.TableMethod == current.TableMethod && a.TruncateBoundary == b.TruncateBoundary
}

// SameRetainedLineage validates already-captured view/query authority, retaining
// strict defining metadata as well as row shape, family and equal boundary.
// It is not the policy for binding a new stable query by current name/ACL.
func SameRetainedLineage(old, current *catalog.Relation, a, b *storage.Relation) bool {
	return sameRetainedFamily(old, current, a, b) && catalog.RelationDefiningAuthorityCompatible(old, current) && a.TruncateBoundary == b.TruncateBoundary
}

// CrossedTruncate selects the distinct current-root/original-frontier birth
// filter policy for a new stable-root query. It does not authorize finishing an
// already captured view, retaining old rows, or publishing against old storage.
// Current-root routing retains ordinary TRUNCATE's compatible name/owner
// metadata behavior; strict defining authority belongs to captured-view checks.
func CrossedTruncate(old, current *catalog.Relation, a, b *storage.Relation) bool {
	return sameRetainedFamily(old, current, a, b) && b.TruncateBoundary > a.TruncateBoundary
}

func sameRetainedFamily(old, current *catalog.Relation, a, b *storage.Relation) bool {
	if old == nil || current == nil || a == nil || b == nil || b.RetainedOrigin == 0 ||
		old.Address() != current.Address() || (old.RelationKind != catalog.TableRelation && old.RelationKind != catalog.MaterializedViewRelation) || old.RelationKind != current.RelationKind ||
		!index.SameRelationRowShape(old, current) {
		return false
	}
	if old.RelationKind == catalog.MaterializedViewRelation && (!old.Populated || !current.Populated) {
		return false
	}
	origin := a.RetainedOrigin
	if origin == 0 {
		origin = old.EffectiveStorageLocator(catalog.Read{})
	}
	return origin != 0 && origin == b.RetainedOrigin
}
