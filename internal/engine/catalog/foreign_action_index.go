package catalog

import (
	"iter"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/util"
)

// This derived index contains only owners of enforced, potentially mutating
// incoming FK actions. Persistent membership follows immutable descriptors;
// it stores no executable demand, permissions, or transaction state.
type foreignActionOwners = util.PersistentMap[ids.ObjectAddress, *Relation]
type foreignActionIndex = util.PersistentMap[ids.ObjectAddress, foreignActionOwners]

func MutatingForeignAction(action parser.ForeignKeyAction) bool {
	return action == parser.ForeignKeyCascade || action == parser.ForeignKeySetNull || action == parser.ForeignKeySetDefault
}

func foreignActionTarget(f ForeignKeyConstraint) bool {
	return !f.NotEnforced && (MutatingForeignAction(f.DeleteAction) || MutatingForeignAction(f.UpdateAction))
}

func hasForeignActionTarget(r *Relation, target ids.ObjectAddress) bool {
	if r != nil {
		for _, f := range r.Constraints.ForeignKeys {
			if f.Target == target && foreignActionTarget(f) {
				return true
			}
		}
	}
	return false
}

func (s *State) updateForeignActionOwners(old, next Descriptor) {
	a, _ := old.(*Relation)
	b, _ := next.(*Relation)
	if a != nil {
		for _, f := range a.Constraints.ForeignKeys {
			if !foreignActionTarget(f) || b != nil && a.Address() == b.Address() && hasForeignActionTarget(b, f.Target) {
				continue
			}
			owners, ok := s.foreignActionOwners.Get(f.Target)
			if !ok {
				continue
			}
			if _, found := owners.Get(a.Address()); !found {
				continue
			}
			owners = owners.Delete(a.Address())
			if owners.Len() == 0 {
				s.foreignActionOwners = s.foreignActionOwners.Delete(f.Target)
			} else {
				s.foreignActionOwners = s.foreignActionOwners.Set(f.Target, owners)
			}
		}
	}
	if b != nil {
		for _, f := range b.Constraints.ForeignKeys {
			if !foreignActionTarget(f) {
				continue
			}
			owners, _ := s.foreignActionOwners.Get(f.Target)
			if current, found := owners.Get(b.Address()); found && current == b {
				continue
			}
			s.foreignActionOwners = s.foreignActionOwners.Set(f.Target, owners.Set(b.Address(), b))
		}
	}
}

// HasIncomingForeignActionOwners is the allocation-free empty-key gate before
// a consumer creates recursive traversal or iterator closure state.
func (r Read) HasIncomingForeignActionOwners(target ids.ObjectAddress) bool {
	if r.state == nil {
		return false
	}
	owners, found := r.state.foreignActionOwners.Get(target)
	return found && owners.Len() != 0
}

// IncomingForeignActionOwners visits exact current child descriptors without a
// catalog scan. A relation may own several matching FKs; consumers select the
// actual action and walk its typed executable closure under current authority.
func (r Read) IncomingForeignActionOwners(target ids.ObjectAddress) iter.Seq2[ids.ObjectAddress, *Relation] {
	if r.state == nil {
		return func(func(ids.ObjectAddress, *Relation) bool) {}
	}
	owners, _ := r.state.foreignActionOwners.Get(target)
	return owners.All()
}
