package engine

import (
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/engine/catalog"
)

// The caller is the affected historical-only NN branch of real TYPE cleanup.
// It has not rebound the primary yet; the ordinary TYPE rebuilder still owns
// fresh key/index identities and supplies the completed binding below.
func physicalNotNullFromType(old, candidate *catalog.Relation, fact catalog.NotNullConstraint) (catalog.PhysicalNotNullProof, bool) {
	if old.Inheritance == nil || fact.OID == 0 || fact.NotValid || fact.NoInherit || !slices.Contains(old.Constraints.NotNull, fact) {
		return catalog.PhysicalNotNullProof{}, false
	}
	primary := catalog.PhysicalPrimaryForAttribute(old, fact.Attribute)
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
	return catalog.PhysicalNotNullProof{Cause: cause, Usage: old.Columns[slot].Usage, Support: catalog.CapturePhysicalPrimarySupport(candidate, primary)}, true
}

func validatePhysicalNotNull(view catalog.Read, table *catalog.Relation, ordinary map[int32]bool) error {
	var validated *catalog.PhysicalPrimarySupport
	for attribute, proof := range table.Constraints.PhysicalNotNull {
		if ordinary[attribute] || !PhysicalNotNullOriginValid(view, table, attribute, proof) {
			return fmt.Errorf("stuntgres: invalid physical not-null origin")
		}
		primary := catalog.PhysicalPrimaryForAttribute(table, attribute)
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
			if !PhysicalPrimarySupportValid(view, table, primary, proof.Support) {
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
	if len(next.Constraints.PhysicalNotNull) == 0 {
		return
	}
	var copied bool
	for attribute := range next.Constraints.PhysicalNotNull {
		slot := next.IndexForAttribute(attribute)
		ordinary := slices.ContainsFunc(next.Constraints.NotNull, func(fact catalog.NotNullConstraint) bool { return fact.Attribute == attribute })
		if slot >= 0 && !next.Columns[slot].Nullable && !ordinary {
			continue
		}
		if !copied {
			next.Constraints.PhysicalNotNull = maps.Clone(next.Constraints.PhysicalNotNull)
			copied = true
		}
		delete(next.Constraints.PhysicalNotNull, attribute)
	}
	if len(next.Constraints.PhysicalNotNull) == 0 {
		next.Constraints.PhysicalNotNull = nil
	}
}

// Rebind only after the existing ordinary TYPE/unique/index providers finish.
// This does not mint constraints, indexes or NN records and performs no row work.
func rebindPhysicalNotNullType(stage *CatalogStage, parent *catalog.Relation, changes []columnTransformation) (*catalog.Relation, error) {
	if len(parent.Constraints.PhysicalNotNull) == 0 {
		return parent, nil
	}
	next := *parent
	prunePhysicalNotNull(&next)
	var support *catalog.PhysicalPrimarySupport
	copied := next.Constraints.PhysicalNotNull != nil && len(next.Constraints.PhysicalNotNull) != len(parent.Constraints.PhysicalNotNull)
	for attribute, proof := range next.Constraints.PhysicalNotNull {
		primary := catalog.PhysicalPrimaryForAttribute(&next, attribute)
		if primary == nil && (proof.Support != nil || proof.Retired == nil && proof.Cause.Kind != catalog.PhysicalNotNullImportFlag) {
			return nil, fmt.Errorf("stuntgres: missing rebuilt physical not-null primary")
		}
		// Included payload TYPE changes can rebuild the real primary group
		// without changing a key's Usage. Follow that actual group replacement.
		touches := primary != nil && proof.Support != nil && (primary.OID != proof.Support.Constraint || primary.Index != proof.Support.Index)
		for _, change := range changes {
			if primary != nil && slices.Contains(primary.Attributes, change.Attribute) || primary == nil && change.Attribute == attribute {
				touches = true
				break
			}
		}
		if !touches {
			continue
		}
		if !copied {
			next.Constraints.PhysicalNotNull = maps.Clone(next.Constraints.PhysicalNotNull)
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
		next.Constraints.PhysicalNotNull[attribute] = proof
	}
	if !copied && len(next.Constraints.PhysicalNotNull) == len(parent.Constraints.PhysicalNotNull) {
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
	copied := len(old.Constraints.PhysicalNotNull) != len(next.Constraints.PhysicalNotNull)
	for attribute, proof := range next.Constraints.PhysicalNotNull {
		if proof.Support == nil || catalog.PhysicalPrimaryForAttribute(next, attribute) != nil {
			continue
		}
		previous := catalog.PhysicalPrimaryForAttribute(old, attribute)
		if previous == nil || previous.OID != proof.Support.Constraint || previous.Index != proof.Support.Index {
			continue
		}
		if !copied {
			next.Constraints.PhysicalNotNull = maps.Clone(next.Constraints.PhysicalNotNull)
			copied = true
		}
		proof.Retired, proof.Support = proof.Support, nil
		next.Constraints.PhysicalNotNull[attribute] = proof
	}
}

// Callers own the detached NN slice and inheritance graph. Physical TYPE cleanup
// and its strict import counterpart retire the same actual definition without
// deciding the independent physical attribute flag here.
func removeDetachedNotNullMetadata(next *catalog.Relation, fact catalog.NotNullConstraint) {
	next.Constraints.NotNull = slices.DeleteFunc(next.Constraints.NotNull, func(candidate catalog.NotNullConstraint) bool { return candidate.OID == fact.OID })
	if next.Inheritance != nil {
		delete(next.Inheritance.NotNull, fact.Attribute)
	}
}
