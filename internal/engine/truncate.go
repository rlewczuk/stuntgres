package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// A granted identity owns both the immutable descriptor and its physical store.
// Virtual partition roots have no store but participate in locks and closure.
type truncateRelation struct {
	ref       ObjectRef
	relation  *relationDescriptor
	storage   *relationStorage
	privilege bool
}

func checkTruncate(auth AuthorizationView, ref ObjectRef, d *relationDescriptor, privilege, only bool) error {
	if guardedCatalogRelation(d) {
		return guardedCatalogMutationError(auth, d, true)
	}
	if d.relationKind == foreignRelation {
		return sqlError("0A000", "foreign table truncation is unsupported in embedded mode")
	}
	if d.relationKind != tableRelation && d.relationKind != partitionedRelation {
		return sqlError("42809", fmt.Sprintf("%q is not a table", d.name))
	}
	if privilege {
		if err := auth.Check(AccessRequirement{Object: ref, Privileges: authorization.TruncatePrivilege}); err != nil {
			return err
		}
	}
	if only && d.partition != nil {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: "cannot truncate only a partitioned table", Hint: "Do not specify the ONLY keyword, or use TRUNCATE ONLY on the partitions directly."}
	}
	return nil
}

// Descendant admission and its final graph fence share the inherited-query
// consumer's namespace/session-token authority, not child ACL or USAGE checks.
func truncateOtherTemporary(catalog CatalogRead, relation *relationDescriptor, token SessionToken) (bool, error) {
	namespace, ok := catalog.state.descriptors[relation.namespace].(*namespaceDescriptor)
	if !ok {
		return false, ErrCatalogReplaced
	}
	return namespace.temporary != (SessionToken{}) && namespace.temporary != token, nil
}

// Explicit targets are opened in user order. Named occurrences always check
// their own ACL, even when an earlier inheritance expansion already selected them.
func (s *StatementScope) acquireTruncate(stmt *parser.Truncate) error {
	e := s.op.session.Engine()
	if !s.op.commandAdmission {
		s.op.snapshotNoticeThreshold()
	}
	selected := make(map[ObjectAddress]int)
	s.truncateSelected = selected
	s.truncateRestart = stmt.RestartIdentity
	expanded := make(map[ObjectAddress]bool)
	var open func(ObjectRef, *truncateName, bool, bool, bool, bool) error
	open = func(ref ObjectRef, name *truncateName, privilege, only, recurse, cascade bool) error {
		e.mu.Lock()
		auth, err := e.statementAuthorizationLocked(s.op, stmt)
		var before *relationDescriptor
		var store *relationStorage
		otherTemporary := false
		if err == nil {
			if ref.owner != e.owner || ref.Epoch != e.state.epoch || s.txRef.epoch != ref.Epoch {
				err = ErrCatalogReplaced
			} else {
				before, _ = auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
				if before == nil {
					err = ErrCatalogReplaced
				} else {
					if !privilege {
						otherTemporary, err = truncateOtherTemporary(auth.catalog, before, s.op.session.token)
					}
					if err == nil && !otherTemporary {
						err = checkTruncate(auth, ref, before, privilege && !cascade, only)
						if err == nil && privilege {
							err = checkTemporaryStorageAccess(auth.catalog, before, s.op.session.token)
						}
						if err == nil {
							store = e.privateStorageLocked(s.owner.catalog, ref.Address)
						}
					}
				}
			}
		}
		e.mu.Unlock()
		if err != nil {
			return err
		}
		if otherTemporary {
			// Do not acquire, select or descend into another session's TEMP branch.
			return nil
		}
		if before.membership != nil {
			if err = s.acquirePartitionAncestors(ref); err != nil {
				return err
			}
		}
		policy := relationLockPolicy{operation: s.op, timeout: s.op.lockTimeout, statementDeadline: s.op.statementDeadline, source: s.op.sourceContext}
		request, err := e.locks.startAcquire(s.op.Context(), s.ticket, lockResource{ObjectRef: ref}, accessExclusive, policy)
		waited := request != nil
		if waited {
			if s.replacementRead {
				e.replacementMu.RUnlock()
				s.replacementRead = false
			}
			err = request.wait()
		}
		if err != nil {
			return err
		}
		if s.implicit && !s.replacementRead {
			e.replacementMu.RLock()
			s.replacementRead = true
		}
		e.mu.Lock()
		auth, err = e.statementAuthorizationLocked(s.op, stmt)
		var d *relationDescriptor
		if err == nil {
			d, _ = auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
			current := e.privateStorageLocked(s.owner.catalog, ref.Address)
			if ref.Epoch != e.state.epoch || s.txRef.epoch != ref.Epoch || d == nil || before.namespace != d.namespace || !sameRelationRowShape(before, d) || store != current && !sameTruncateLineage(store, current) {
				err = ErrCatalogReplaced
			} else if name != nil {
				err = e.truncateNameCurrent(auth, *name)
			}
			if err == nil && !privilege {
				otherTemporary, err = truncateOtherTemporary(auth.catalog, d, s.op.session.token)
				if err == nil && otherTemporary {
					err = ErrCatalogReplaced
				}
			}
			if err == nil && cascade {
				// Records are detached to the adapter later; this invokes no user callback.
				s.op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf("truncate cascades to table %q", d.name)})
			}
			if err == nil {
				err = checkTruncate(auth, ref, d, privilege, only)
			}
			if err == nil {
				err = s.relationCursorPreflight(auth.catalog, ref.Address, "TRUNCATE")
			}
			if err == nil {
				if namespace, ok := auth.catalog.state.descriptors[d.namespace].(*namespaceDescriptor); ok && namespace.temporary == s.op.session.token {
					s.owner.accessedTemporary = true
				}
				if index := selected[ref.Address]; index == 0 {
					s.truncateRelations = append(s.truncateRelations, truncateRelation{ref: ref, relation: d, storage: current, privilege: privilege})
					selected[ref.Address] = len(s.truncateRelations)
				} else if privilege {
					s.truncateRelations[index-1].privilege = true
				}
			}
		}
		var children []*relationDescriptor
		if err == nil && recurse && !expanded[ref.Address] {
			expanded[ref.Address] = true
			children = inheritanceChildren(auth.catalog, ref.Address)
			if d.partition != nil {
				for _, address := range d.partition.children {
					child, ok := auth.catalog.state.descriptors[address].(*relationDescriptor)
					if !ok {
						err = ErrCatalogReplaced
						break
					}
					children = append(children, child)
				}
				slices.SortFunc(children, func(a, b *relationDescriptor) int { return compareTruncateAddress(a.address, b.address) })
			}
		}
		e.mu.Unlock()
		if err != nil {
			return err
		}
		for _, child := range children {
			if err = open(ObjectRef{owner: ref.owner, Epoch: ref.Epoch, Address: child.address}, nil, false, false, true, false); err != nil {
				return err
			}
		}
		return nil
	}
	for _, target := range stmt.Targets {
		e.mu.Lock()
		auth, err := e.statementAuthorizationLocked(s.op, stmt)
		var ref ObjectRef
		observation := truncateName{name: target.Name}
		if err == nil && s.txRef.epoch != e.state.epoch {
			err = ErrCatalogReplaced
		}
		if err == nil {
			ref, err = e.resolveRelationReference(auth, target.Name, true)
		}
		if err == nil {
			descriptor, ok := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
			if !ok {
				err = ErrCatalogReplaced
			} else {
				observation.ref, observation.namespace = ref, descriptor.namespace
			}
		}
		e.mu.Unlock()
		if err != nil {
			return err
		}
		if err = open(ref, &observation, true, target.Only, !target.Only, false); err != nil {
			return err
		}
		s.truncateNames = append(s.truncateNames, observation)
	}
	for {
		e.mu.Lock()
		catalog := e.privateCatalogLocked(s.owner.catalog)
		inbound := truncateReferencers(catalog, selected)
		e.mu.Unlock()
		if len(inbound) == 0 {
			if stmt.RestartIdentity {
				return s.acquireTruncateSequences(stmt)
			}
			return nil
		}
		if stmt.Mode != parser.DropCascade {
			return truncateForeignKeyError(inbound[0].parent.name, inbound[0].relation.name)
		}
		for _, candidate := range inbound {
			ref := ObjectRef{owner: e.owner, Epoch: s.txRef.epoch, Address: candidate.relation.address}
			if err := open(ref, nil, true, false, false, true); err != nil {
				return err
			}
		}
	}
}

type truncateReferencer struct{ relation, parent *relationDescriptor }

func compareTruncateAddress(a, b ObjectAddress) int {
	if a.ObjectID < b.ObjectID {
		return -1
	}
	if a.ObjectID > b.ObjectID {
		return 1
	}
	return 0
}

// FK definitions alone decide closure, including empty referencing tables.
func truncateReferencers(catalog CatalogRead, selected map[ObjectAddress]int) []truncateReferencer {
	var result []truncateReferencer
	for address, descriptor := range catalog.state.descriptors {
		d, ok := descriptor.(*relationDescriptor)
		if !ok || selected[address] != 0 {
			continue
		}
		for _, fk := range d.foreignKeys {
			if selected[fk.Target] != 0 {
				parent, _ := catalog.state.descriptors[fk.Target].(*relationDescriptor)
				if parent != nil {
					result = append(result, truncateReferencer{d, parent})
				}
				break
			}
		}
	}
	slices.SortFunc(result, func(a, b truncateReferencer) int {
		return compareTruncateAddress(a.relation.address, b.relation.address)
	})
	return result
}
func truncateForeignKeyError(parent, child string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot truncate a table referenced in a foreign key constraint", Detail: fmt.Sprintf("Table %q references %q.", child, parent), Hint: fmt.Sprintf("Truncate table %q at the same time, or use TRUNCATE ... CASCADE.", child)}
}
