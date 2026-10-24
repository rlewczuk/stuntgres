package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

// roleStore owns the engine's role state: the principal domain, the ACL
// authority, the committed role publication and the principal allocator. It
// has no mutex. Engine.mu guards every field, and mutating methods carry the Locked
// suffix.
//
// Cluster runtimes share one pointer-stable store:
// catalog.Owner.Principals, catalog.Owner.ACLAuthority and
// Root.Reprovenance keep pointers to its domain and ACL authority.
//
// Published is the committed record. It changes only through
// PublishLocked, and the principal and revision frontiers only through
// their Install…Locked writers and AdvanceRevisionFrontierLocked, so every
// publication point stays greppable.
// Transaction overlays of the role publication live in session.Journal, not
// here; published is only the committed record.
type roleStore struct {
	Domain        authorization.Domain
	ACLAuthority  sqltype.ACLAuthority
	Published     *access.RoleRecord
	nextPrincipal uint64
	nextRevision  uint64
}

// PublishLocked installs next as the committed role record. Every role
// publication goes through it, so publication points stay greppable.
func (s *roleStore) PublishLocked(next *access.RoleRecord) { s.Published = next }

// NextPrincipal returns the principal allocation frontier.
func (s *roleStore) NextPrincipal() uint64 { return s.nextPrincipal }

// InstallPrincipalFrontierLocked sets the principal frontier, after a
// published CREATE ROLE or for a private stage.
func (s *roleStore) InstallPrincipalFrontierLocked(next uint64) { s.nextPrincipal = next }

// NextRevision returns the authorization revision frontier.
func (s *roleStore) NextRevision() uint64 { return s.nextRevision }

// InstallRevisionFrontierLocked sets the authorization revision frontier
// after a reserved candidate revision publishes.
func (s *roleStore) InstallRevisionFrontierLocked(next uint64) { s.nextRevision = next }

// Initialize seeds the predefined roles and the principal allocator. It runs
// once, while the engine is not yet shared.
func (s *roleStore) Initialize() {
	s.Published = &access.RoleRecord{Root: authorization.Seed(&s.Domain), Revision: 1}
	s.nextPrincipal = authorization.LastPredefinedRoleID + 1
	s.nextRevision = 2
}

// PrincipalCandidateLocked returns a private candidate; only successful
// publication advances allocation.
func (s *roleStore) PrincipalCandidateLocked() (authorization.PrincipalRef, error) {
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
func (s *roleStore) Read(owner *catalog.Owner, view catalog.Read, root *access.RoleRecord) authorization.Read {
	return access.RoleRead(owner, &s.Domain, view, root)
}

func (s *roleStore) RevisionCandidateLocked() (uint64, error) {
	if s.Published.Revision == ^uint64(0) || s.nextRevision == ^uint64(0) {
		return 0, fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	return max(s.nextRevision, s.Published.Revision+1), nil
}

// PublishedRead returns the role view of the committed record as seen by
// view's current database owner.
func (s *roleStore) PublishedRead(owner *catalog.Owner, view catalog.Read) authorization.Read {
	return s.Read(owner, view, s.Published)
}

// AdvanceRevisionFrontierLocked moves the revision frontier past revision. It
// never moves the frontier back, and it saturates at the last revision.
func (s *roleStore) AdvanceRevisionFrontierLocked(revision uint64) {
	if revision < s.nextRevision {
		return
	}
	s.nextRevision = revision
	if revision != ^uint64(0) {
		s.nextRevision++
	}
}

// roleCapture is a store's committed record and principal frontier, captured
// together under Engine.mu.
type roleCapture struct {
	record        *access.RoleRecord
	nextPrincipal uint64
}

// CaptureLocked captures the committed record and the principal frontier.
func (s *roleStore) CaptureLocked() roleCapture {
	return roleCapture{record: s.Published, nextPrincipal: s.nextPrincipal}
}

// InstallCapture publishes a captured record in this store's domain, keeping
// its revision and principal frontier. It returns the source-to-target
// principal map of the copy. The store must not be shared yet.
func (s *roleStore) InstallCapture(c roleCapture) map[authorization.PrincipalRef]authorization.PrincipalRef {
	targets := make(map[authorization.PrincipalRef]authorization.PrincipalRef, c.record.Root.Read(authorization.PrincipalRef{}).RoleCount())
	s.Published = &access.RoleRecord{Root: c.record.Root.Reprovenance(&s.Domain, targets), Revision: c.record.Revision}
	s.nextPrincipal = c.nextPrincipal
	return targets
}
