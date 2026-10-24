package engine

import (
	"iter"
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
)

func roleDependencyReferences(base catalog.Read, descriptors iter.Seq2[ids.ObjectAddress, catalog.Descriptor], defaults iter.Seq2[catalog.DefaultACLKey, *catalog.DefaultACLRecord]) iter.Seq[authorization.PrincipalRef] {
	return func(yield func(authorization.PrincipalRef) bool) {
		for address, descriptor := range descriptors {
			if descriptor == nil || descriptor == base.Get(address) {
				continue
			}
			before := base.Get(address)
			for ref := range access.DescriptorRoleReferences(descriptor) {
				if before != nil && access.DescriptorRoleDependencies(before, ref, nil) {
					continue
				}
				if !yield(ref) {
					return
				}
			}
		}
		for key, record := range defaults {
			before := base.State().DefaultACL(key)
			if record == nil || record == before {
				continue
			}
			for ref := range access.DefaultACLRoleReferences(key, record) {
				present := false
				for prior := range access.DefaultACLRoleReferences(key, before) {
					if prior == ref {
						present = true
						break
					}
				}
				if !present && !yield(ref) {
					return
				}
			}
		}
	}
}

// No role root or catalog candidate is installed before these ordinary AS
// resources are admitted and their exact principal identities are rechecked.
func (s *StatementScope) acquireRoleDependencies(references iter.Seq[authorization.PrincipalRef]) error {
	var scratch [16]authorization.PrincipalRef
	seen := scratch[:0]
	var large map[authorization.PrincipalRef]bool
	for ref := range references {
		if ref == (authorization.PrincipalRef{}) {
			continue
		}
		if large != nil {
			if large[ref] {
				continue
			}
			large[ref] = true
		} else {
			if slices.Contains(seen, ref) {
				continue
			}
			if len(seen) == len(scratch) {
				large = make(map[authorization.PrincipalRef]bool, len(seen)+1)
				for _, prior := range seen {
					large[prior] = true
				}
				large[ref] = true
			} else {
				seen = append(seen, ref)
			}
		}
		e := s.engine()
		e.mu.Lock()
		_, present := e.sessionRolesLocked(s.op.Session).Root.Read(authorization.PrincipalRef{}).Find(ref)
		e.mu.Unlock()
		if !present {
			return catalog.Conflict{}
		}
		if err := s.acquireRolePrincipal(ref, lock.AccessShare, false); err != nil {
			return err
		}
		if err := s.roleStillPresent(ref); err != nil {
			return err
		}
	}
	return nil
}

func (s *StatementScope) acquireStageRoleDependencies(stage *CatalogStage) error {
	return s.acquireRoleDependencies(roleDependencyReferences(stage.Base, stage.Descriptors(), stage.DefaultACLs()))
}

// Caller holds Engine.mu; restore that ownership on every exit. Manual catalog
// lanes join the same statement ticket before retaining their candidate journal.
func (s *StatementScope) acquireJournalRoleDependenciesLocked(changes *session.Journal) error {
	e := s.engine()
	base := e.privateCatalogLocked(s.owner.Catalog)
	references := roleDependencyReferences(base, func(yield func(ids.ObjectAddress, catalog.Descriptor) bool) {
		for address, descriptor := range changes.Descriptors {
			if !yield(address, descriptor) {
				return
			}
		}
	}, func(yield func(catalog.DefaultACLKey, *catalog.DefaultACLRecord) bool) {
		for key, record := range changes.DefaultACLs {
			if !yield(key, record) {
				return
			}
		}
	})
	e.mu.Unlock()
	err := s.acquireRoleDependencies(references)
	e.mu.Lock()
	return err
}
