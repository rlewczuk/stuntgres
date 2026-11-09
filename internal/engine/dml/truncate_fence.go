package dml

import (
	"iter"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

// ReconcileMode reports how TruncateGraph.ReconcileDescriptors projected the
// descriptors a check must examine.
type ReconcileMode uint8

const (
	// ReconcileExact needs no descriptors because the effective receipt is the
	// exact current version.
	ReconcileExact ReconcileMode = iota
	// ReconcileDelta enumerates only journal descriptor edits after the receipt.
	ReconcileDelta
	// ReconcileFull conservatively returns the whole current inventory.
	ReconcileFull
)

// TruncateGraph is the graph a TRUNCATE admitted, recorded in its catalog
// fence: the original descriptor version it read, the selected relations, the
// session whose TEMP descendants it excluded, and with RESTART IDENTITY the
// committed generators of the owned sequences it staged. Base never advances.
// Checked is a separate receipt from a successful whole catalog preflight;
// ReceiptBase is the committed base version used to project that receipt and
// ReceiptMark the journal descriptor-edit position at certification. Replacing
// them requires a fresh fence value, never mutation of a shared fence.
// Targets and Sequences are immutable, and sequences are not counter undo.
type TruncateGraph struct {
	Base        catalog.DescriptorVersion
	Checked     catalog.DescriptorVersion
	ReceiptBase catalog.DescriptorVersion
	ReceiptMark session.DescriptorEditMark
	Targets     map[ids.ObjectAddress]int
	Token       ids.SessionToken
	Restart     bool
	Sequences   map[ids.ObjectAddress]*storage.SequenceGenerator
}

// noDescriptors is the exact-mode descriptor source.
func noDescriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor] {
	return func(func(ids.ObjectAddress, catalog.Descriptor) bool) {}
}

// ReconcileDescriptors returns the descriptors a check must examine and how
// they were selected. It is exact only when the effective receipt matches
// current. The delta is authenticated against the committed base, the
// journal's cached private projection and its descriptor-edit history, and is
// rejected when an edit changes or removes a namespace's TEMP authority. Every
// other case conservatively falls back to the whole current inventory.
func (g TruncateGraph) ReconcileDescriptors(current, base catalog.Read, journal *session.Journal) (iter.Seq2[ids.ObjectAddress, catalog.Descriptor], ReconcileMode) {
	effective := g.Checked
	if effective == (catalog.DescriptorVersion{}) {
		effective = g.Base
	}
	if !effective.SameAuthority(base) {
		return (catalog.DescriptorVersion{}).DescriptorsToReconcile(current), ReconcileFull
	}
	if effective.Matches(current) {
		return noDescriptors(), ReconcileExact
	}
	if effective.SameAuthority(current) && g.ReceiptBase.Matches(base) && journal != nil &&
		journal.CachedBase == base.State() && journal.Cached.State() == current.State() {
		full := false
		trusted := journal.DescriptorEditsSince(g.ReceiptMark, func(address ids.ObjectAddress) bool {
			if address.ClassID != ids.NamespaceClassID {
				return true
			}
			old, oldOK := effective.Get(address).(*catalog.Namespace)
			cur, curOK := current.Get(address).(*catalog.Namespace)
			if !oldOK {
				// The address held no namespace at the receipt. A relation under
				// an absent namespace would have failed that whole preflight, and
				// a relation newly referencing this one is itself in the delta.
				return true
			}
			if !curOK || old.Temporary != cur.Temporary {
				full = true
				return false
			}
			// A replacement preserving Temporary (rename, ACL, comment, owner,
			// GRANT, repair) leaves every graph-visible namespace input intact.
			return true
		})
		if trusted && !full {
			return g.deltaDescriptors(current, journal), ReconcileDelta
		}
	}
	return effective.DescriptorsToReconcile(current), ReconcileFull
}

// deltaDescriptors re-walks the authenticated edit suffix, yielding each
// still-present current descriptor. It allocates no result slice; a repeated
// edit of one address yields that address repeatedly.
func (g TruncateGraph) deltaDescriptors(current catalog.Read, journal *session.Journal) iter.Seq2[ids.ObjectAddress, catalog.Descriptor] {
	return func(yield func(ids.ObjectAddress, catalog.Descriptor) bool) {
		journal.DescriptorEditsSince(g.ReceiptMark, func(address ids.ObjectAddress) bool {
			descriptor := current.Get(address)
			if descriptor == nil {
				return true
			}
			return yield(address, descriptor)
		})
	}
}

// CheckCurrent reports catalog.Conflict when the graph changed under journal:
// catalog replacements do not have to honor SQL AX locks, so it fences new
// inbound FK and hierarchy edges as well as the exact selected descriptors,
// stores and, with Restart, the owned sequences. current is the journal's
// private catalog, base the committed catalog it projects, and published the
// published sequence generators. It runs at publication with Engine.mu held
// and takes no lock.
func (g TruncateGraph) CheckCurrent(current, base catalog.Read, journal *session.Journal, published map[ids.ObjectAddress]*storage.SequenceGenerator) error {
	if g.Restart {
		for address, generator := range g.Sequences {
			if published[address] != generator {
				return catalog.Conflict{}
			}
		}
	}
	descriptors, _ := g.ReconcileDescriptors(current, base, journal)
	if g.Restart {
		for address, descriptor := range descriptors {
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
	for address, descriptor := range descriptors {
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
