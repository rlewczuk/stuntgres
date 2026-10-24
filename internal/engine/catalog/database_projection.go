package catalog

import (
	"errors"
	"iter"

	"stuntgres/internal/engine/ids"
)

// Database descriptors are cluster objects: every store of a cluster carries
// the same ones, each rooted in its own domain. The helpers below keep them in
// step. They take the stores of the cluster as an iterator, so the common
// publication without a database change allocates nothing. The engine's mutex
// is held throughout.

var errDatabaseIdentityExhausted = errors.New("stuntgres: database identity exhausted")

// RetainDatabasesLocked replaces the database descriptors of next, an
// unpublished state, with the store's published ones. Attachment identity and
// ACLs belong to the destination, not to portable state.
func (s *Store) RetainDatabasesLocked(next *State) {
	for address, descriptor := range next.Descriptors() {
		if address.ClassID == ids.DatabaseClassID {
			next.UnbindName(DescriptorNameKey(descriptor))
			next.RemoveDescriptor(address)
		}
	}
	for address, descriptor := range s.State.Descriptors() {
		if address.ClassID == ids.DatabaseClassID {
			next.SetDescriptor(address, descriptor)
			next.BindName(DescriptorNameKey(descriptor), address)
		}
	}
}

// DatabaseProjection is the prepared roots of one database descriptor change,
// one per store. A nil projection installs nothing.
type DatabaseProjection map[*Store]*State

// ProjectDatabasesLocked prepares the roots of every store before any is
// installed, when next, the store's own unpublished state, changes a database
// descriptor. stores yields every store of the cluster, s included; next is
// s's root. Descriptor publications are shared.
func (s *Store) ProjectDatabasesLocked(next *State, stores iter.Seq[*Store]) DatabaseProjection {
	var changed []Descriptor
	for address, descriptor := range next.Descriptors() {
		if address.ClassID == ids.DatabaseClassID && s.State.Get(address) != descriptor {
			changed = append(changed, descriptor)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	var roots DatabaseProjection
	for target := range stores {
		root := next
		if target != s {
			root = CloneState(target.State)
		}
		for _, descriptor := range changed {
			if old := root.Get(descriptor.Address()); old != nil {
				root.UnbindName(DescriptorNameKey(old))
			}
			root.SetDescriptor(descriptor.Address(), descriptor)
			root.BindName(DescriptorNameKey(descriptor), descriptor.Address())
		}
		if roots == nil {
			roots = DatabaseProjection{}
		}
		roots[target] = root
	}
	return roots
}

// InstallLocked publishes every prepared root.
func (p DatabaseProjection) InstallLocked() {
	for target, root := range p {
		target.PublishLocked(root)
	}
}

// DatabaseOIDLocked reserves a database OID at or past start and beyond the
// allocation frontiers of target and others, which include transaction-private
// allocations not yet in a published root. Imported definitions may lie beyond
// those frontiers, so it inspects the published roots too. No frontier
// changes: the caller advances start only when the database is published.
func DatabaseOIDLocked(start ids.OID, target *Store, others iter.Seq[*Store]) (ids.OID, error) {
	oid := start
	advance := func(s *Store) bool {
		if s.nextOID == 0 {
			return false
		}
		oid = max(oid, s.nextOID)
		return true
	}
	if oid == 0 || !advance(target) {
		return 0, errDatabaseIdentityExhausted
	}
	for s := range others {
		if !advance(s) {
			return 0, errDatabaseIdentityExhausted
		}
	}
	used := func(s *Store) bool {
		for address, descriptor := range s.State.Descriptors() {
			if (address.ClassID != ids.LargeObjectClassID && address.ObjectID == oid) || DefinitionOIDUsed(descriptor, oid) {
				return true
			}
		}
		for _, record := range s.State.DefaultACLs() {
			if record != nil && record.OID == oid {
				return true
			}
		}
		return false
	}
	for oid != ^ids.OID(0) {
		reserved := used(target)
		for s := range others {
			if reserved {
				break
			}
			reserved = used(s)
		}
		if !reserved {
			return oid, nil
		}
		oid++
	}
	return 0, errDatabaseIdentityExhausted
}
