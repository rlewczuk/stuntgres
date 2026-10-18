package engine

import (
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func physicalPrimaryForAttribute(table *catalog.Relation, attribute int32) *catalog.UniqueConstraint {
	for i := range table.UniqueConstraints {
		constraint := &table.UniqueConstraints[i]
		if constraint.Primary && slices.Contains(constraint.Attributes, attribute) {
			return constraint
		}
	}
	return nil
}

func capturePhysicalPrimarySupport(table *catalog.Relation, primary *catalog.UniqueConstraint) *catalog.PhysicalPrimarySupport {
	for _, proof := range table.PhysicalNotNull {
		if proof.Support != nil && proof.Support.Constraint == primary.OID && proof.Support.Index == primary.Index {
			return proof.Support
		}
	}
	keys := make([]catalog.PhysicalPrimaryKeyUsage, len(primary.Attributes))
	for i, attribute := range primary.Attributes {
		keys[i] = catalog.PhysicalPrimaryKeyUsage{attribute, table.Columns[table.IndexForAttribute(attribute)].Usage}
	}
	return &catalog.PhysicalPrimarySupport{Constraint: primary.OID, Index: primary.Index, Keys: keys}
}

// The caller is the affected historical-only NN branch of real TYPE cleanup.
// It has not rebound the primary yet; the ordinary TYPE rebuilder still owns
// fresh key/index identities and supplies the completed binding below.
func physicalNotNullFromType(old, candidate *catalog.Relation, fact catalog.NotNullConstraint) (catalog.PhysicalNotNullProof, bool) {
	if old.Inheritance == nil || fact.OID == 0 || fact.NotValid || fact.NoInherit || !slices.Contains(old.NotNullConstraints, fact) {
		return catalog.PhysicalNotNullProof{}, false
	}
	primary := physicalPrimaryForAttribute(old, fact.Attribute)
	if primary == nil {
		return catalog.PhysicalNotNullProof{}, false
	}
	slot := old.IndexForAttribute(fact.Attribute)
	provenance := old.Inheritance.NotNull[fact.Attribute]
	if slot < 0 || provenance.Local || len(provenance.Origins) != 0 || len(provenance.Retired) == 0 || old.Columns[slot].Nullable || !old.Columns[slot].NullabilityKnown {
		return catalog.PhysicalNotNullProof{}, false
	}
	for _, origin := range provenance.Retired {
		if origin.Own != fact.OID {
			return catalog.PhysicalNotNullProof{}, false
		}
	}
	parent := provenance.Retired[0].Parent
	ref := parent.Domain().Ref(parent.Epoch, old.Address())
	cause := &catalog.PhysicalNotNullCause{Kind: catalog.PhysicalNotNullTypeCleanup, Ref: ref, Origin: ref, Attribute: fact.Attribute, RemovedNN: fact.OID, Primary: primary.OID, Index: primary.Index, Usage: old.Columns[slot].Usage}
	cause.Self = cause
	return catalog.PhysicalNotNullProof{Cause: cause, Usage: old.Columns[slot].Usage, Support: capturePhysicalPrimarySupport(candidate, primary)}, true
}

func physicalNotNullOriginValid(view CatalogRead, table *catalog.Relation, attribute int32, proof catalog.PhysicalNotNullProof) bool {
	cause := proof.Cause
	if cause == nil || cause.Self != cause || cause.Kind != catalog.PhysicalNotNullTypeCleanup && cause.Kind != catalog.PhysicalNotNullImportCleanup && cause.Kind != catalog.PhysicalNotNullImportFlag || cause.Ref.Address != table.Address() || cause.Ref.Address.SubID != 0 || cause.Attribute != attribute || cause.RemovedNN == 0 || cause.Usage.Type == nil || !storedReferenceEpochValid(view, table, cause.Ref) {
		return false
	}
	if cause.Origin.Domain() == nil || cause.Origin.Epoch == 0 || cause.Origin.Address.ClassID != ids.RelationClassID || cause.Origin.Address.ObjectID == 0 || cause.Origin.Address.SubID != 0 {
		return false
	}
	if cause.Kind == catalog.PhysicalNotNullImportFlag {
		if cause.Primary != 0 || cause.Index != (ids.ObjectAddress{}) {
			return false
		}
	} else if cause.Primary == 0 || cause.Index.ClassID != ids.RelationClassID || cause.Index.ObjectID == 0 || cause.Index.SubID != 0 {
		return false
	}
	slot := table.IndexForAttribute(attribute)
	return table.RelationKind == catalog.TableRelation && table.Partition == nil && table.Membership == nil && slot >= 0 && table.Columns[slot].NullabilityKnown && !table.Columns[slot].Nullable && proof.Usage == table.Columns[slot].Usage
}

func physicalPrimarySupportValid(view CatalogRead, table *catalog.Relation, primary *catalog.UniqueConstraint, support *catalog.PhysicalPrimarySupport) bool {
	if support == nil || primary == nil || !primary.Primary || support.Constraint != primary.OID || support.Index != primary.Index || len(support.Keys) != len(primary.Attributes) {
		return false
	}
	index, ok := view.Relation(primary.Index)
	if !ok || index.RelationKind != catalog.IndexRelation || index.Namespace() != table.Namespace() || index.Owner != table.Owner || index.SQLIndex == nil || index.SQLIndex.Parent != table.Address() || index.SQLIndex.Constraint == nil || index.SQLIndex.Constraint.Kind != catalog.UniqueBackingKind || index.SQLIndex.Constraint.OID != primary.OID || index.SQLIndex.Constraint.Index != primary.Index || index.SQLIndex.Constraint.Name != primary.Name || !index.SQLIndex.Constraint.Primary || len(index.SQLIndex.Keys) != len(support.Keys) || len(index.Columns) < len(support.Keys) {
		return false
	}
	for i, key := range support.Keys {
		slot := table.IndexForAttribute(key.Attribute)
		attribute, expression, err := indexCatalogKey(table, index.SQLIndex.Keys[i])
		if slot < 0 || key.Attribute != primary.Attributes[i] || key.Usage != table.Columns[slot].Usage || key.Usage != index.Columns[i].Usage || err != nil || expression != nil || attribute != key.Attribute {
			return false
		}
	}
	return true
}

func validatePhysicalNotNull(view CatalogRead, table *catalog.Relation, ordinary map[int32]bool) error {
	var validated *catalog.PhysicalPrimarySupport
	for attribute, proof := range table.PhysicalNotNull {
		if ordinary[attribute] || !physicalNotNullOriginValid(view, table, attribute, proof) {
			return fmt.Errorf("stuntgres: invalid physical not-null origin")
		}
		primary := physicalPrimaryForAttribute(table, attribute)
		if proof.Support == nil {
			if primary != nil {
				return fmt.Errorf("stuntgres: missing physical not-null primary binding")
			}
			if proof.Retired == nil {
				if proof.Cause.Kind != catalog.PhysicalNotNullImportFlag {
					return fmt.Errorf("stuntgres: missing physical not-null retirement")
				}
			} else if proof.Retired.Constraint == 0 || proof.Retired.Index.ObjectID == 0 || !slices.ContainsFunc(proof.Retired.Keys, func(key catalog.PhysicalPrimaryKeyUsage) bool { return key.Attribute == attribute }) {
				return fmt.Errorf("stuntgres: invalid retired physical not-null support")
			}
			continue
		}
		if primary == nil {
			return fmt.Errorf("stuntgres: missing physical not-null primary support")
		}
		if proof.Support != validated {
			if !physicalPrimarySupportValid(view, table, primary, proof.Support) {
				return fmt.Errorf("stuntgres: invalid physical not-null primary support")
			}
			validated = proof.Support
		}
	}
	return nil
}

// Real NN creation/column retirement supersedes the exceptional physical flag.
// Callers already own a detached relation candidate; the old map stays immutable.
func prunePhysicalNotNull(next *catalog.Relation) {
	if len(next.PhysicalNotNull) == 0 {
		return
	}
	var copied bool
	for attribute := range next.PhysicalNotNull {
		slot := next.IndexForAttribute(attribute)
		ordinary := slices.ContainsFunc(next.NotNullConstraints, func(fact catalog.NotNullConstraint) bool { return fact.Attribute == attribute })
		if slot >= 0 && !next.Columns[slot].Nullable && !ordinary {
			continue
		}
		if !copied {
			next.PhysicalNotNull = maps.Clone(next.PhysicalNotNull)
			copied = true
		}
		delete(next.PhysicalNotNull, attribute)
	}
	if len(next.PhysicalNotNull) == 0 {
		next.PhysicalNotNull = nil
	}
}

// Rebind only after the existing ordinary TYPE/unique/index providers finish.
// This does not mint constraints, indexes or NN records and performs no row work.
func rebindPhysicalNotNullType(stage *CatalogStage, parent *catalog.Relation, changes []columnTransformation) (*catalog.Relation, error) {
	if len(parent.PhysicalNotNull) == 0 {
		return parent, nil
	}
	next := *parent
	prunePhysicalNotNull(&next)
	var support *catalog.PhysicalPrimarySupport
	copied := next.PhysicalNotNull != nil && len(next.PhysicalNotNull) != len(parent.PhysicalNotNull)
	for attribute, proof := range next.PhysicalNotNull {
		primary := physicalPrimaryForAttribute(&next, attribute)
		if primary == nil && (proof.Support != nil || proof.Retired == nil && proof.Cause.Kind != catalog.PhysicalNotNullImportFlag) {
			return nil, fmt.Errorf("stuntgres: missing rebuilt physical not-null primary")
		}
		// Included payload TYPE changes can rebuild the real primary group
		// without changing a key's Usage. Follow that actual group replacement.
		touches := primary != nil && proof.Support != nil && (primary.OID != proof.Support.Constraint || primary.Index != proof.Support.Index)
		for _, change := range changes {
			if primary != nil && slices.Contains(primary.Attributes, change.attribute) || primary == nil && change.attribute == attribute {
				touches = true
				break
			}
		}
		if !touches {
			continue
		}
		if !copied {
			next.PhysicalNotNull = maps.Clone(next.PhysicalNotNull)
			copied = true
		}
		if primary != nil && support == nil {
			// The primary is the real rebuilt group. Do not reuse its old key
			// Usage merely because a stale proof still names the same position.
			keys := make([]catalog.PhysicalPrimaryKeyUsage, len(primary.Attributes))
			for i, key := range primary.Attributes {
				keys[i] = catalog.PhysicalPrimaryKeyUsage{key, next.Columns[next.IndexForAttribute(key)].Usage}
			}
			support = &catalog.PhysicalPrimarySupport{Constraint: primary.OID, Index: primary.Index, Keys: keys}
		}
		proof.Usage = next.Columns[next.IndexForAttribute(attribute)].Usage
		if primary != nil {
			proof.Support = support
		}
		next.PhysicalNotNull[attribute] = proof
	}
	if !copied && len(next.PhysicalNotNull) == len(parent.PhysicalNotNull) {
		return parent, nil
	}
	if err := stage.Replace(&next); err != nil {
		return nil, err
	}
	return &next, nil
}

// A genuine supported DROP may retire the key, but it does not clear the
// independently surviving physical attribute flag or revive an NN row.
func transitionPhysicalNotNull(old, next *catalog.Relation) {
	prunePhysicalNotNull(next)
	copied := len(old.PhysicalNotNull) != len(next.PhysicalNotNull)
	for attribute, proof := range next.PhysicalNotNull {
		if proof.Support == nil || physicalPrimaryForAttribute(next, attribute) != nil {
			continue
		}
		previous := physicalPrimaryForAttribute(old, attribute)
		if previous == nil || previous.OID != proof.Support.Constraint || previous.Index != proof.Support.Index {
			continue
		}
		if !copied {
			next.PhysicalNotNull = maps.Clone(next.PhysicalNotNull)
			copied = true
		}
		proof.Retired, proof.Support = proof.Support, nil
		next.PhysicalNotNull[attribute] = proof
	}
}

// Callers own the detached NN slice and inheritance graph. Physical TYPE cleanup
// and its strict import counterpart retire the same actual definition without
// deciding the independent physical attribute flag here.
func removeDetachedNotNullMetadata(next *catalog.Relation, fact catalog.NotNullConstraint) {
	next.NotNullConstraints = slices.DeleteFunc(next.NotNullConstraints, func(candidate catalog.NotNullConstraint) bool { return candidate.OID == fact.OID })
	if next.Inheritance != nil {
		delete(next.Inheritance.NotNull, fact.Attribute)
	}
}
