package access

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

// RoleStore owns the engine's role state: the principal domain, the ACL
// authority, the committed role publication and the principal, revision and
// membership OID allocators. It has no mutex. The engine guards every field, and
// mutating methods carry the Locked suffix.
//
// Cluster runtimes share one pointer-stable store:
// catalog.Owner.Principals, catalog.Owner.ACLAuthority and
// Root.Reprovenance keep pointers to its domain and ACL authority, so a
// store must never be copied. go vet's copylocks check reports copies.
//
// Published is the committed record. It changes only through
// PublishLocked, and allocation frontiers only through
// their Install…Locked writers and AdvanceRevisionFrontierLocked, so every
// publication point stays greppable. Transaction overlays of the role
// publication live in session.Journal, not here.
type RoleStore struct {
	_             noCopy
	Domain        authorization.Domain
	ACLAuthority  sqltype.ACLAuthority
	Published     *RoleRecord
	nextPrincipal uint64
	nextRevision  uint64
	nextMembershipOID uint64
}

// noCopy makes go vet's copylocks check report copies of the struct that
// holds it. It has no state.
type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}

// PublishLocked installs next as the committed role record. Every role
// publication goes through it, so publication points stay greppable.
func (s *RoleStore) PublishLocked(next *RoleRecord) { s.Published = next }

// NextPrincipal returns the principal allocation frontier.
func (s *RoleStore) NextPrincipal() uint64 { return s.nextPrincipal }

// InstallPrincipalFrontierLocked sets the principal frontier, after a
// published CREATE ROLE or for a private stage.
func (s *RoleStore) InstallPrincipalFrontierLocked(next uint64) { s.nextPrincipal = next }

// NextRevision returns the authorization revision frontier.
func (s *RoleStore) NextRevision() uint64 { return s.nextRevision }

// InstallRevisionFrontierLocked sets the authorization revision frontier
// after a reserved candidate revision publishes.
func (s *RoleStore) InstallRevisionFrontierLocked(next uint64) { s.nextRevision = next }

// NextMembershipOID returns the engine-wide membership reservation frontier.
func (s *RoleStore) NextMembershipOID() uint64 { return s.nextMembershipOID }

// InstallMembershipFrontierLocked publishes accepted-operation reservations.
// Transaction and savepoint rollback must never rewind this frontier.
func (s *RoleStore) InstallMembershipFrontierLocked(next uint64) { s.nextMembershipOID = next }

// Initialize seeds the predefined roles and the principal allocator. It runs
// once, while the engine is not yet shared.
func (s *RoleStore) Initialize() {
	s.Published = &RoleRecord{Root: authorization.Seed(&s.Domain), Revision: 1}
	s.nextPrincipal = authorization.LastPredefinedRoleID + 1
	s.nextRevision = 2
	s.nextMembershipOID = authorization.FirstMembershipOID + 3
}

// PrincipalCandidateLocked returns a private candidate; only successful
// publication advances allocation.
func (s *RoleStore) PrincipalCandidateLocked() (authorization.PrincipalRef, error) {
	id := s.nextPrincipal
	if id == 0 || id == ^uint64(0) {
		return authorization.PrincipalRef{}, fmt.Errorf("stuntgres: principal identity or authorization revision exhausted")
	}
	if id <= authorization.LastPredefinedRoleID {
		id = authorization.LastPredefinedRoleID + 1
	}
	ref := s.Domain.Principal(id)
	if _, exists := s.Published.Root.Read(authorization.PrincipalRef{}).Find(ref); exists {
		return authorization.PrincipalRef{}, fmt.Errorf("stuntgres: principal identity already allocated")
	}
	return ref, nil
}

// Read returns the role view of root as seen by view's current database
// owner. It is empty unless view belongs to owner, the catalog owner this
// store backs, and root belongs to this store's domain.
func (s *RoleStore) Read(owner *catalog.Owner, view catalog.Read, root *RoleRecord) authorization.Read {
	return RoleRead(owner, &s.Domain, view, root)
}

func (s *RoleStore) RevisionCandidateLocked() (uint64, error) {
	if s.Published.Revision == ^uint64(0) || s.nextRevision == ^uint64(0) {
		return 0, fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	return max(s.nextRevision, s.Published.Revision+1), nil
}

// PublishedRead returns the role view of the committed record as seen by
// view's current database owner.
func (s *RoleStore) PublishedRead(owner *catalog.Owner, view catalog.Read) authorization.Read {
	return s.Read(owner, view, s.Published)
}

// AdvanceRevisionFrontierLocked moves the revision frontier past revision. It
// never moves the frontier back, and it saturates at the last revision.
func (s *RoleStore) AdvanceRevisionFrontierLocked(revision uint64) {
	if revision < s.nextRevision {
		return
	}
	s.nextRevision = revision
	if revision != ^uint64(0) {
		s.nextRevision++
	}
}

// RoleCapture is a committed record and principal/membership frontiers, captured
// together under Engine.mu.
type RoleCapture struct {
	record        *RoleRecord
	nextPrincipal uint64
	nextMembershipOID uint64
}

// CaptureLocked captures the committed record and identity frontiers.
func (s *RoleStore) CaptureLocked() RoleCapture {
	return RoleCapture{record: s.Published, nextPrincipal: s.nextPrincipal, nextMembershipOID: s.nextMembershipOID}
}

// InstallCapture publishes a captured record in this store's domain, keeping
// its revision and identity frontiers. It returns the source-to-target
// principal map of the copy. The store must not be shared yet.
func (s *RoleStore) InstallCapture(c RoleCapture) map[authorization.PrincipalRef]authorization.PrincipalRef {
	targets := make(map[authorization.PrincipalRef]authorization.PrincipalRef, c.record.Root.Read(authorization.PrincipalRef{}).RoleCount())
	s.Published = &RoleRecord{Root: c.record.Root.Reprovenance(&s.Domain, targets), Revision: c.record.Revision}
	s.nextPrincipal = c.nextPrincipal
	s.nextMembershipOID = c.nextMembershipOID
	return targets
}
