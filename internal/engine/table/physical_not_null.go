package table

import (
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/index"
)

// The caller is the affected historical-only NN branch of real TYPE cleanup.
// It has not rebound the primary yet; the ordinary TYPE rebuilder still owns
// fresh key/index identities and supplies the completed binding below.
func PhysicalNotNullFromType(old, candidate *catalog.Relation, fact catalog.NotNullConstraint) (catalog.PhysicalNotNullProof, bool) {
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

func ValidatePhysicalNotNull(view catalog.Read, table *catalog.Relation, ordinary map[int32]bool) error {
	var validated *catalog.PhysicalPrimarySupport
	for attribute, proof := range table.Constraints.PhysicalNotNull {
		if ordinary[attribute] || !index.PhysicalNotNullOriginValid(view, table, attribute, proof) {
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
			if !index.PhysicalPrimarySupportValid(view, table, primary, proof.Support) {
				return fmt.Errorf("stuntgres: invalid physical not-null primary support")
			}
			validated = proof.Support
		}
	}
	return nil
}

// Real NN creation/column retirement supersedes the exceptional physical flag.
// Callers already own a detached relation candidate; the old map stays immutable.
func PrunePhysicalNotNull(next *catalog.Relation) {
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

// A genuine supported DROP may retire the key, but it does not clear the
// independently surviving physical attribute flag or revive an NN row.
func TransitionPhysicalNotNull(old, next *catalog.Relation) {
	PrunePhysicalNotNull(next)
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
func RemoveDetachedNotNullMetadata(next *catalog.Relation, fact catalog.NotNullConstraint) {
	next.Constraints.NotNull = slices.DeleteFunc(next.Constraints.NotNull, func(candidate catalog.NotNullConstraint) bool { return candidate.OID == fact.OID })
	if next.Inheritance != nil {
		delete(next.Inheritance.NotNull, fact.Attribute)
	}
}
