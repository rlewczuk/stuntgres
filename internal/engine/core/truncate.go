package core

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Explicit targets are opened in user order. Named occurrences always check
// their own ACL, even when an earlier inheritance expansion already selected them.
func (s *StatementScope) acquireTruncate(stmt *parser.Truncate) error {
	e := s.Engine()
	if !s.Op.CommandAdmission {
		s.Op.SnapshotNoticeThreshold()
	}
	selected := make(map[ids.ObjectAddress]int)
	s.truncate().Selected = selected
	s.truncate().Restart = stmt.RestartIdentity
	s.truncate().ForeignCascade = stmt.Mode == parser.DropCascade
	expanded := make(map[ids.ObjectAddress]bool)
	var open func(ids.ObjectRef, *dml.TruncateName, bool, bool, bool, bool) error
	open = func(ref ids.ObjectRef, name *dml.TruncateName, privilege, only, recurse, cascade bool) error {
		e.Mu.Lock()
		auth, err := e.StatementAuthorizationLocked(s.Op, stmt)
		var before *catalog.Relation
		var store *storage.Relation
		otherTemporary := false
		if err == nil {
			if ref.Domain() != e.Catalog.Domain || ref.Epoch != e.Catalog.State.Epoch() || s.TxRef.Epoch() != ref.Epoch {
				err = ids.ErrCatalogReplaced
			} else {
				before, _ = auth.Catalog.Relation(ref.Address)
				if before == nil {
					err = ids.ErrCatalogReplaced
				} else {
					if !privilege {
						otherTemporary, err = dml.TruncateOtherTemporary(auth.Catalog, before, s.Op.Session.SessionToken)
					}
					if err == nil && !otherTemporary {
						err = dml.CheckTruncate(auth.View, auth, ref, before, privilege && !cascade, only)
						if err == nil && privilege {
							err = access.CheckTemporaryStorageAccess(auth.Catalog, before, s.Op.Session.SessionToken)
						}
						if err == nil {
							store = e.PrivateStorageLocked(s.Owner.Catalog, ref.Address)
						}
					}
				}
			}
		}
		e.Mu.Unlock()
		if err != nil {
			return err
		}
		if otherTemporary {
			// Do not acquire, select or descend into another session's TEMP branch.
			return nil
		}
		if before.Membership != nil {
			if err = s.acquirePartitionAncestors(ref); err != nil {
				return err
			}
		}
		policy := lock.Policy{TimeoutSlot: &s.Op.LockWait, Timeout: s.Op.LockTimeout, StatementDeadline: s.Op.StatementDeadline, Source: s.Op.SourceContext}
		request, err := e.Locks.StartAcquire(s.Op.Context(), s.LockTicket, lock.Resource{ObjectRef: ref}, lock.AccessExclusive, policy)
		waited := request != nil
		if waited {
			if s.ReplacementRead {
				e.ReplacementMu.RUnlock()
				s.ReplacementRead = false
			}
			err = request.Wait()
		}
		if err != nil {
			return err
		}
		if s.implicit && !s.ReplacementRead {
			e.ReplacementMu.RLock()
			s.ReplacementRead = true
		}
		e.Mu.Lock()
		auth, err = e.StatementAuthorizationLocked(s.Op, stmt)
		var d *catalog.Relation
		if err == nil {
			d, _ = auth.Catalog.Relation(ref.Address)
			current := e.PrivateStorageLocked(s.Owner.Catalog, ref.Address)
			if ref.Epoch != e.Catalog.State.Epoch() || s.TxRef.Epoch() != ref.Epoch || d == nil || before.Namespace() != d.Namespace() || !index.SameRelationRowShape(before, d) || store != current && !store.SameTruncateLineage(current) {
				err = ids.ErrCatalogReplaced
			} else if name != nil {
				err = e.truncateNameCurrent(auth, *name)
			}
			if err == nil && !privilege {
				otherTemporary, err = dml.TruncateOtherTemporary(auth.Catalog, d, s.Op.Session.SessionToken)
				if err == nil && otherTemporary {
					err = ids.ErrCatalogReplaced
				}
			}
			if err == nil && cascade {
				// Records are detached to the adapter later; this invokes no user callback.
				s.Op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf("truncate cascades to table %q", d.Name())})
			}
			if err == nil {
				err = dml.CheckTruncate(auth.View, auth, ref, d, privilege, only)
			}
			if err == nil {
				err = s.relationCursorPreflight(auth.Catalog, ref.Address, "TRUNCATE")
			}
			if err == nil {
				if namespace, ok := auth.Catalog.Namespace(d.Namespace()); ok && namespace.Temporary == s.Op.Session.SessionToken {
					s.Owner.AccessedTemporary = true
				}
				if index := selected[ref.Address]; index == 0 {
					s.truncate().Relations = append(s.truncateOrZero().Relations, dml.TruncateRelation{Ref: ref, Relation: d, Storage: current, Privilege: privilege})
					selected[ref.Address] = len(s.truncateOrZero().Relations)
				} else if privilege {
					s.truncate().Relations[index-1].Privilege = true
				}
			}
		}
		var children []*catalog.Relation
		if err == nil && recurse && !expanded[ref.Address] {
			expanded[ref.Address] = true
			children = catalog.InheritanceChildren(auth.Catalog, ref.Address)
			if d.Partition != nil {
				for _, address := range d.Partition.Children {
					child, ok := auth.Catalog.Relation(address)
					if !ok {
						err = ids.ErrCatalogReplaced
						break
					}
					children = append(children, child)
				}
				slices.SortFunc(children, func(a, b *catalog.Relation) int { return dml.CompareTruncateAddress(a.Address(), b.Address()) })
			}
		}
		e.Mu.Unlock()
		if err != nil {
			return err
		}
		for _, child := range children {
			if err = open(ref.Domain().Ref(ref.Epoch, child.Address()), nil, false, false, true, false); err != nil {
				return err
			}
		}
		return nil
	}
	for _, target := range stmt.Targets {
		e.Mu.Lock()
		auth, err := e.StatementAuthorizationLocked(s.Op, stmt)
		var ref ids.ObjectRef
		observation := dml.TruncateName{Name: target.Name}
		if err == nil && s.TxRef.Epoch() != e.Catalog.State.Epoch() {
			err = ids.ErrCatalogReplaced
		}
		if err == nil {
			ref, err = e.ResolveRelationReference(auth, target.Name, true)
		}
		if err == nil {
			descriptor, ok := auth.Catalog.Relation(ref.Address)
			if !ok {
				err = ids.ErrCatalogReplaced
			} else {
				observation.Ref, observation.Namespace = ref, descriptor.Namespace()
			}
		}
		e.Mu.Unlock()
		if err != nil {
			return err
		}
		if err = open(ref, &observation, true, target.Only, !target.Only, false); err != nil {
			return err
		}
		s.truncate().Names = append(s.truncateOrZero().Names, observation)
	}
	for {
		e.Mu.Lock()
		catalog := e.PrivateCatalogLocked(s.Owner.Catalog)
		inbound := dml.TruncateReferencers(catalog, selected)
		e.Mu.Unlock()
		if len(inbound) == 0 {
			if stmt.RestartIdentity {
				return s.acquireTruncateSequences(stmt)
			}
			return nil
		}
		if stmt.Mode != parser.DropCascade {
			return dml.TruncateForeignKeyError(inbound[0].Parent.Name(), inbound[0].Relation.Name())
		}
		for _, candidate := range inbound {
			ref := e.Catalog.Domain.Ref(s.TxRef.Epoch(), candidate.Relation.Address())
			if err := open(ref, nil, true, false, false, true); err != nil {
				return err
			}
		}
	}
}

// truncate returns the statement's truncate slot, allocating it on first use.
func (s *StatementScope) truncate() *dml.TruncateScope {
	if s.truncateSlot == nil {
		s.truncateSlot = new(dml.TruncateScope)
	}
	return s.truncateSlot
}

// truncateOrZero reads the truncate slot; a nil slot reads as zero.
func (s *StatementScope) truncateOrZero() dml.TruncateScope {
	if s.truncateSlot == nil {
		return dml.TruncateScope{}
	}
	return *s.truncateSlot
}
