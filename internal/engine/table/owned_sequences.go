package table

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
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

// OwnedSequences calls prepare, then removes, each sequence that a target of
// the drop owns and the drop has not already removed.
func OwnedSequences(provider DropDependencyReader, targets []ids.ObjectAddress, prepare func(ids.ObjectRef) error) error {
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
		if err := prepare(ref); err != nil {
			return err
		}
		if err := provider.Remove(ref); err != nil {
			return err
		}
		if r.Sequence.Identity {
			provider.TombstoneIdentity(a)
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

// OwnedExpressionDependencies removes the sequences that targets own, after
// the expression dependencies of each sequence. Inheritance tables the drop
// already removed count as targets too.
func OwnedExpressionDependencies(provider DropDependencyReader, targets []ids.ObjectAddress, mode parser.DropMode) error {
	var closure []ids.ObjectAddress
	extended := false
	// Include removed hierarchy owners, not unrelated removals staged by an
	// earlier pass. Ordinary column projection retains its existing ordering.
	for address, descriptor := range provider.RemovalDescriptors() {
		relation, ok := descriptor.(*catalog.Relation)
		if !ok || relation.RelationKind != catalog.TableRelation || !catalog.InheritanceParticipant(provider.BaseRead(), relation) {
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
	return OwnedSequences(provider, targets, func(ref ids.ObjectRef) error {
		if closure == nil {
			closure = make([]ids.ObjectAddress, len(targets)+1)
			copy(closure, targets)
		}
		closure[len(targets)] = ref.Address
		if err := provider.ExpressionDependencies(closure, mode, ids.ObjectAddress{}); err != nil {
			return err
		}
		if provider.StatementBound() {
			return provider.Acquire(ref, false)
		}
		return nil
	})
}
