package engine

import (
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type physicalNotNullCauseKind uint8

const (
	physicalNotNullTypeCleanup physicalNotNullCauseKind = iota + 1
	physicalNotNullImportCleanup
	physicalNotNullImportFlag
)

// A missing NN row is not a generic alternative to an NN constraint. This
// immutable cause is issued only by real TYPE cleanup or its exact logical
// import counterpart. Archived identities are not current provider edges.
type physicalNotNullCause struct {
	self      *physicalNotNullCause
	kind      physicalNotNullCauseKind
	ref       ids.ObjectRef
	origin    ids.ObjectRef // Immutable archive namespace; never resolved as a provider.
	attribute int32
	removedNN ids.OID
	primary   ids.OID
	index     ids.ObjectAddress
	usage     sqltype.Usage
}
type physicalPrimaryKeyUsage struct {
	attribute int32
	usage     sqltype.Usage
}
type physicalPrimarySupport struct {
	constraint ids.OID
	index      ids.ObjectAddress
	keys       []physicalPrimaryKeyUsage
}
type physicalNotNullProof struct {
	cause   *physicalNotNullCause
	usage   sqltype.Usage
	support *physicalPrimarySupport
	retired *physicalPrimarySupport // Genuine later key retirement, not a live edge.
}

func physicalPrimaryForAttribute(table *relationDescriptor, attribute int32) *uniqueConstraint {
	for i := range table.uniqueConstraints {
		constraint := &table.uniqueConstraints[i]
		if constraint.Primary && slices.Contains(constraint.Attributes, attribute) {
			return constraint
		}
	}
	return nil
}

func capturePhysicalPrimarySupport(table *relationDescriptor, primary *uniqueConstraint) *physicalPrimarySupport {
	for _, proof := range table.physicalNotNull {
		if proof.support != nil && proof.support.constraint == primary.OID && proof.support.index == primary.Index {
			return proof.support
		}
	}
	keys := make([]physicalPrimaryKeyUsage, len(primary.Attributes))
	for i, attribute := range primary.Attributes {
		keys[i] = physicalPrimaryKeyUsage{attribute, table.columns[table.indexForAttribute(attribute)].Usage}
	}
	return &physicalPrimarySupport{constraint: primary.OID, index: primary.Index, keys: keys}
}

// The caller is the affected historical-only NN branch of real TYPE cleanup.
// It has not rebound the primary yet; the ordinary TYPE rebuilder still owns
// fresh key/index identities and supplies the completed binding below.
func physicalNotNullFromType(old, candidate *relationDescriptor, fact notNullConstraint) (physicalNotNullProof, bool) {
	if old.inheritance == nil || fact.OID == 0 || fact.NotValid || fact.NoInherit || !slices.Contains(old.notNullConstraints, fact) {
		return physicalNotNullProof{}, false
	}
	primary := physicalPrimaryForAttribute(old, fact.Attribute)
	if primary == nil {
		return physicalNotNullProof{}, false
	}
	slot := old.indexForAttribute(fact.Attribute)
	provenance := old.inheritance.notNull[fact.Attribute]
	if slot < 0 || provenance.local || len(provenance.origins) != 0 || len(provenance.retired) == 0 || old.columns[slot].Nullable || !old.columns[slot].NullabilityKnown {
		return physicalNotNullProof{}, false
	}
	for _, origin := range provenance.retired {
		if origin.own != fact.OID {
			return physicalNotNullProof{}, false
		}
	}
	parent := provenance.retired[0].parent
	ref := parent.Domain().Ref(parent.Epoch, old.address)
	cause := &physicalNotNullCause{kind: physicalNotNullTypeCleanup, ref: ref, origin: ref, attribute: fact.Attribute, removedNN: fact.OID, primary: primary.OID, index: primary.Index, usage: old.columns[slot].Usage}
	cause.self = cause
	return physicalNotNullProof{cause: cause, usage: old.columns[slot].Usage, support: capturePhysicalPrimarySupport(candidate, primary)}, true
}

func physicalNotNullOriginValid(view CatalogRead, table *relationDescriptor, attribute int32, proof physicalNotNullProof) bool {
	cause := proof.cause
	if cause == nil || cause.self != cause || cause.kind != physicalNotNullTypeCleanup && cause.kind != physicalNotNullImportCleanup && cause.kind != physicalNotNullImportFlag || cause.ref.Address != table.address || cause.ref.Address.SubID != 0 || cause.attribute != attribute || cause.removedNN == 0 || cause.usage.Type == nil || !storedReferenceEpochValid(view, table, cause.ref) {
		return false
	}
	if cause.origin.Domain() == nil || cause.origin.Epoch == 0 || cause.origin.Address.ClassID != ids.RelationClassID || cause.origin.Address.ObjectID == 0 || cause.origin.Address.SubID != 0 {
		return false
	}
	if cause.kind == physicalNotNullImportFlag {
		if cause.primary != 0 || cause.index != (ids.ObjectAddress{}) {
			return false
		}
	} else if cause.primary == 0 || cause.index.ClassID != ids.RelationClassID || cause.index.ObjectID == 0 || cause.index.SubID != 0 {
		return false
	}
	slot := table.indexForAttribute(attribute)
	return table.relationKind == tableRelation && table.partition == nil && table.membership == nil && slot >= 0 && table.columns[slot].NullabilityKnown && !table.columns[slot].Nullable && proof.usage == table.columns[slot].Usage
}

func physicalPrimarySupportValid(view CatalogRead, table *relationDescriptor, primary *uniqueConstraint, support *physicalPrimarySupport) bool {
	if support == nil || primary == nil || !primary.Primary || support.constraint != primary.OID || support.index != primary.Index || len(support.keys) != len(primary.Attributes) {
		return false
	}
	index, ok := view.Relation(primary.Index)
	if !ok || index.relationKind != indexRelation || index.namespace != table.namespace || index.owner != table.owner || index.sqlIndex == nil || index.sqlIndex.parent != table.address || index.sqlIndex.constraint == nil || index.sqlIndex.constraint.kind != uniqueBackingKind || index.sqlIndex.constraint.OID != primary.OID || index.sqlIndex.constraint.Index != primary.Index || index.sqlIndex.constraint.Name != primary.Name || !index.sqlIndex.constraint.Primary || len(index.sqlIndex.keys) != len(support.keys) || len(index.columns) < len(support.keys) {
		return false
	}
	for i, key := range support.keys {
		slot := table.indexForAttribute(key.attribute)
		attribute, expression, err := indexCatalogKey(table, index.sqlIndex.keys[i])
		if slot < 0 || key.attribute != primary.Attributes[i] || key.usage != table.columns[slot].Usage || key.usage != index.columns[i].Usage || err != nil || expression != nil || attribute != key.attribute {
			return false
		}
	}
	return true
}

func validatePhysicalNotNull(view CatalogRead, table *relationDescriptor, ordinary map[int32]bool) error {
	var validated *physicalPrimarySupport
	for attribute, proof := range table.physicalNotNull {
		if ordinary[attribute] || !physicalNotNullOriginValid(view, table, attribute, proof) {
			return fmt.Errorf("stuntgres: invalid physical not-null origin")
		}
		primary := physicalPrimaryForAttribute(table, attribute)
		if proof.support == nil {
			if primary != nil {
				return fmt.Errorf("stuntgres: missing physical not-null primary binding")
			}
			if proof.retired == nil {
				if proof.cause.kind != physicalNotNullImportFlag {
					return fmt.Errorf("stuntgres: missing physical not-null retirement")
				}
			} else if proof.retired.constraint == 0 || proof.retired.index.ObjectID == 0 || !slices.ContainsFunc(proof.retired.keys, func(key physicalPrimaryKeyUsage) bool { return key.attribute == attribute }) {
				return fmt.Errorf("stuntgres: invalid retired physical not-null support")
			}
			continue
		}
		if primary == nil {
			return fmt.Errorf("stuntgres: missing physical not-null primary support")
		}
		if proof.support != validated {
			if !physicalPrimarySupportValid(view, table, primary, proof.support) {
				return fmt.Errorf("stuntgres: invalid physical not-null primary support")
			}
			validated = proof.support
		}
	}
	return nil
}

// Real NN creation/column retirement supersedes the exceptional physical flag.
// Callers already own a detached relation candidate; the old map stays immutable.
func prunePhysicalNotNull(next *relationDescriptor) {
	if len(next.physicalNotNull) == 0 {
		return
	}
	var copied bool
	for attribute := range next.physicalNotNull {
		slot := next.indexForAttribute(attribute)
		ordinary := slices.ContainsFunc(next.notNullConstraints, func(fact notNullConstraint) bool { return fact.Attribute == attribute })
		if slot >= 0 && !next.columns[slot].Nullable && !ordinary {
			continue
		}
		if !copied {
			next.physicalNotNull = maps.Clone(next.physicalNotNull)
			copied = true
		}
		delete(next.physicalNotNull, attribute)
	}
	if len(next.physicalNotNull) == 0 {
		next.physicalNotNull = nil
	}
}

// Rebind only after the existing ordinary TYPE/unique/index providers finish.
// This does not mint constraints, indexes or NN records and performs no row work.
func rebindPhysicalNotNullType(stage *CatalogStage, parent *relationDescriptor, changes []columnTransformation) (*relationDescriptor, error) {
	if len(parent.physicalNotNull) == 0 {
		return parent, nil
	}
	next := *parent
	prunePhysicalNotNull(&next)
	var support *physicalPrimarySupport
	copied := next.physicalNotNull != nil && len(next.physicalNotNull) != len(parent.physicalNotNull)
	for attribute, proof := range next.physicalNotNull {
		primary := physicalPrimaryForAttribute(&next, attribute)
		if primary == nil && (proof.support != nil || proof.retired == nil && proof.cause.kind != physicalNotNullImportFlag) {
			return nil, fmt.Errorf("stuntgres: missing rebuilt physical not-null primary")
		}
		// Included payload TYPE changes can rebuild the real primary group
		// without changing a key's Usage. Follow that actual group replacement.
		touches := primary != nil && proof.support != nil && (primary.OID != proof.support.constraint || primary.Index != proof.support.index)
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
			next.physicalNotNull = maps.Clone(next.physicalNotNull)
			copied = true
		}
		if primary != nil && support == nil {
			// The primary is the real rebuilt group. Do not reuse its old key
			// Usage merely because a stale proof still names the same position.
			keys := make([]physicalPrimaryKeyUsage, len(primary.Attributes))
			for i, key := range primary.Attributes {
				keys[i] = physicalPrimaryKeyUsage{key, next.columns[next.indexForAttribute(key)].Usage}
			}
			support = &physicalPrimarySupport{constraint: primary.OID, index: primary.Index, keys: keys}
		}
		proof.usage = next.columns[next.indexForAttribute(attribute)].Usage
		if primary != nil {
			proof.support = support
		}
		next.physicalNotNull[attribute] = proof
	}
	if !copied && len(next.physicalNotNull) == len(parent.physicalNotNull) {
		return parent, nil
	}
	if err := stage.Replace(&next); err != nil {
		return nil, err
	}
	return &next, nil
}

// A genuine supported DROP may retire the key, but it does not clear the
// independently surviving physical attribute flag or revive an NN row.
func transitionPhysicalNotNull(old, next *relationDescriptor) {
	prunePhysicalNotNull(next)
	copied := len(old.physicalNotNull) != len(next.physicalNotNull)
	for attribute, proof := range next.physicalNotNull {
		if proof.support == nil || physicalPrimaryForAttribute(next, attribute) != nil {
			continue
		}
		previous := physicalPrimaryForAttribute(old, attribute)
		if previous == nil || previous.OID != proof.support.constraint || previous.Index != proof.support.index {
			continue
		}
		if !copied {
			next.physicalNotNull = maps.Clone(next.physicalNotNull)
			copied = true
		}
		proof.retired, proof.support = proof.support, nil
		next.physicalNotNull[attribute] = proof
	}
}

// Callers own the detached NN slice and inheritance graph. Physical TYPE cleanup
// and its strict import counterpart retire the same actual definition without
// deciding the independent physical attribute flag here.
func removeDetachedNotNullMetadata(next *relationDescriptor, fact notNullConstraint) {
	next.notNullConstraints = slices.DeleteFunc(next.notNullConstraints, func(candidate notNullConstraint) bool { return candidate.OID == fact.OID })
	if next.inheritance != nil {
		delete(next.inheritance.notNull, fact.Attribute)
	}
}
