package table

import (
	"iter"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// SequenceOwnedBy is the canonical AUTO ownership match for both closure
// discovery and sequence retirement. SubIDs are stable attributes, not slots.
func SequenceOwnedBy(relation *catalog.Relation, target ids.ObjectAddress) bool {
	if relation.RelationKind != catalog.SequenceRelation || relation.Sequence.Owned == (ids.ObjectAddress{}) {
		return false
	}
	owned := relation.Sequence.Owned
	return owned.ClassID == target.ClassID && owned.ObjectID == target.ObjectID && (target.SubID == 0 || owned.SubID == target.SubID)
}

// SelectOwnedSequences is the canonical read-only AUTO selector. It changes no
// liveness or metadata and yields the real current sequence and captured ref.
func SelectOwnedSequences(provider DropDependencyReader, targets []ids.ObjectAddress) iter.Seq2[ids.ObjectRef, *catalog.Relation] {
	return func(yield func(ids.ObjectRef, *catalog.Relation) bool) {
		for a, d := range provider.InputRelations() {
			if provider.Removed(a) {
				continue
			}
			if replacement := provider.Replacement(a); replacement != nil {
				d = replacement
			}
			r, ok := d.(*catalog.Relation)
			if !ok {
				continue
			}
			remove := false
			for _, target := range targets {
				if SequenceOwnedBy(r, target) {
					remove = true
					break
				}
			}
			if !remove {
				continue
			}
			ref := provider.BaseRef(a)
			if !yield(ref, r) {
				return
			}
		}
	}
}

// OwnedSequences prepares and removes each actual selected sequence.
func OwnedSequences(provider DropDependencyReader, targets []ids.ObjectAddress, prepare func(ids.ObjectRef) error) error {
	for ref, relation := range SelectOwnedSequences(provider, targets) {
		if err := prepare(ref); err != nil {
			return err
		}
		if err := provider.Remove(ref); err != nil {
			return err
		}
		if relation.Sequence.Identity {
			provider.TombstoneIdentity(ref.Address)
		}
	}
	return nil
}

// RemoveColumnSequences consumes already-preflighted and admitted column
// ownership effects. It deliberately does not rediscover dependencies or grant
// resources after column effects have begun.
func RemoveColumnSequences(provider DropDependencyReader, targets []ids.ObjectAddress) error {
	return OwnedSequences(provider, targets, func(ids.ObjectRef) error { return nil })
}

// OwnedSequenceTargets extends only with this phase's authentic removed
// foreign/hierarchy owners. An execution preparation may supply its captured
// actual effect descriptors here without pretending those owners are removed.
func OwnedSequenceTargets(base catalog.Read, targets []ids.ObjectAddress, removals iter.Seq2[ids.ObjectAddress, catalog.Descriptor]) []ids.ObjectAddress {
	extended := false
	for address, descriptor := range removals {
		relation, ok := descriptor.(*catalog.Relation)
		if !ok || relation.RelationKind != catalog.ForeignRelation && (relation.RelationKind != catalog.TableRelation || !catalog.InheritanceParticipant(base, relation)) {
			continue
		}
		present := false
		for _, target := range targets {
			present = present || target == address
		}
		if !present {
			if !extended {
				targets = append([]ids.ObjectAddress(nil), targets...)
				extended = true
			}
			targets = append(targets, address)
		}
	}
	return targets
}
