package catalog

import (
	"errors"
	"iter"

	"stuntgres/internal/engine/ids"
)

// Database, subscription and parameter descriptors are immutable cluster
// objects projected into each database's own domain. Preparation uses each
// peer's current root; a publication without shared changes allocates nothing.
// The cluster mutex is held throughout.

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

// sharedClass reports whether a addresses a cluster object projected into each
// database's own domain.
func sharedClass(a ids.ObjectAddress) bool {
	return a.ClassID == ids.DatabaseClassID || a.ClassID == ids.SubscriptionClassID || a.ClassID == ids.ReplicationOriginClassID || a.ClassID == ids.ParameterClassID
}

// A state keeps the entries of the shared classes in one more persistent map,
// so publication compares them without visiting every descriptor.
// SetDescriptor, RemoveDescriptor and Candidate.State keep it equal to the
// matching descriptor entries; copies share it like the descriptor map.
func (s *State) updateShared(a ids.ObjectAddress, d Descriptor) {
	if !sharedClass(a) {
		return
	}
	if d == nil {
		s.shared = s.shared.Delete(a)
	} else if current, found := s.shared.Get(a); !found || current != d {
		s.shared = s.shared.Set(a, d)
	}
}

// SharedProjection contains prepared roots, one per affected store.
// A nil projection installs nothing.
type SharedProjection map[*Store]*State

// ProjectSharedLocked prepares all roots before any installation. stores yields
// every attached store, including s; next is s's unpublished root.
func (s *Store) ProjectSharedLocked(next *State, stores iter.Seq[*Store]) SharedProjection {
	next.mustNotBulkLoad()
	s.State.mustNotBulkLoad()
	if next.descriptors == s.State.descriptors && next.ParameterIncarnation() == s.State.ParameterIncarnation() {
		return nil
	}
	// The shared maps hold only database, subscription, origin and parameter
	// entries, so a local-only publication visits none of the others.
	if next.ParameterIncarnation() == s.State.ParameterIncarnation() && next.shared.Len() == s.State.shared.Len() &&
		next.shared.Every(func(address ids.ObjectAddress, descriptor Descriptor) bool {
			current, _ := s.State.shared.Get(address)
			return current == descriptor
		}) {
		return nil
	}
	var changed []Descriptor
	var removed []Descriptor
	for address, descriptor := range next.shared.All() {
		if current, _ := s.State.shared.Get(address); current != descriptor {
			changed = append(changed, descriptor)
		}
	}
	for address, descriptor := range s.State.shared.All() {
		if _, found := next.shared.Get(address); !found {
			removed = append(removed, descriptor)
		}
	}
	var roots SharedProjection
	for target := range stores {
		root := next
		if target != s {
			root = CloneState(target.State)
		}
		root.SetParameterIncarnation(next.ParameterIncarnation())
		for _, descriptor := range removed {
			root.UnbindName(DescriptorNameKey(descriptor))
			root.RemoveDescriptor(descriptor.Address())
		}
		for _, descriptor := range changed {
			if old := root.Get(descriptor.Address()); old != nil {
				root.UnbindName(DescriptorNameKey(old))
			}
			root.SetDescriptor(descriptor.Address(), descriptor)
			root.BindName(DescriptorNameKey(descriptor), descriptor.Address())
		}
		if roots == nil {
			roots = SharedProjection{}
		}
		roots[target] = root
	}
	return roots
}

// InstallLocked publishes every prepared root.
func (p SharedProjection) InstallLocked() {
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
			if (address.ClassID != ids.LargeObjectClassID && address.ClassID != ids.ReplicationOriginClassID && address.ObjectID == oid) || DefinitionOIDUsed(descriptor, oid) {
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
