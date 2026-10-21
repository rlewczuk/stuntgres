package table

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// OwnedSequences calls prepare, then removes, each sequence that a target of
// the drop owns and the drop has not already removed.
func OwnedSequences(provider DropDependencyReader, targets []ids.ObjectAddress, prepare func(ids.ObjectRef) error) error {
	for a, d := range provider.InputDescriptors() {
		if provider.Removed(a) {
			continue
		}
		if replacement := provider.Replacement(a); replacement != nil {
			d = replacement
		}
		r, ok := d.(*catalog.Relation)
		if !ok || r.RelationKind != catalog.SequenceRelation || r.Sequence.Owned == (ids.ObjectAddress{}) {
			continue
		}
		owned := r.Sequence.Owned
		remove := false
		for _, target := range targets {
			if owned.ClassID == target.ClassID && owned.ObjectID == target.ObjectID && (target.SubID == 0 || owned.SubID == target.SubID) {
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
	}
	return nil
}
