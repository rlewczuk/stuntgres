package dml

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

// TruncateGraph is the graph a TRUNCATE admitted, recorded in its catalog
// fence: the catalog state it read, the selected relations, the session whose
// TEMP descendants it excluded, and with RESTART IDENTITY the committed
// generators of the owned sequences it staged. It is immutable after
// acquisition; savepoints share Targets, and Sequences holds committed
// bindings, not counter undo.
type TruncateGraph struct {
	Base      *catalog.State
	Targets   map[ids.ObjectAddress]int
	Token     ids.SessionToken
	Restart   bool
	Sequences map[ids.ObjectAddress]*storage.SequenceGenerator
}

// CheckCurrent reports catalog.Conflict when the graph changed under journal:
// catalog replacements do not have to honor SQL AX locks, so it fences new
// inbound FK and hierarchy edges as well as the exact selected descriptors,
// stores and, with Restart, the owned sequences. current is the journal's
// private catalog and published the published sequence generators. It runs at
// publication with Engine.mu held and takes no lock.
func (g TruncateGraph) CheckCurrent(current catalog.Read, journal *session.Journal, published map[ids.ObjectAddress]*storage.SequenceGenerator) error {
	if g.Restart {
		for address, generator := range g.Sequences {
			if published[address] != generator {
				return catalog.Conflict{}
			}
		}
		for address, descriptor := range current.Descriptors() {
			d, ok := descriptor.(*catalog.Relation)
			if !ok || !TruncateOwnsSequence(d, g.Targets) {
				continue
			}
			if _, observed := g.Sequences[address]; observed {
				continue
			}
			// Legitimate own subsequent CREATE/OWNED BY is not an incoming
			// host edge. Its first committed predecessor is fenced by the
			// ordinary originals check, including changes hidden by overlays.
			if _, own := journal.Descriptors[address]; !own {
				return catalog.Conflict{}
			}
		}
	}
	for address, descriptor := range current.Descriptors() {
		if descriptor == g.Base.Get(address) || g.Targets[address] != 0 {
			continue
		}
		d, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		for _, fk := range d.Constraints.ForeignKeys {
			if g.Targets[fk.Target] != 0 {
				return catalog.Conflict{}
			}
		}
		if d.Inheritance != nil || d.Membership != nil {
			otherTemporary, err := TruncateOtherTemporary(current, d, g.Token)
			if err != nil {
				return catalog.Conflict{}
			}
			if otherTemporary {
				continue
			}
		}
		if d.Inheritance != nil {
			for _, parent := range d.Inheritance.Parents {
				if g.Targets[parent.Relation] != 0 {
					return catalog.Conflict{}
				}
			}
		}
		if d.Membership != nil && g.Targets[d.Membership.Parent] != 0 {
			return catalog.Conflict{}
		}
	}
	return nil
}
