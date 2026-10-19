package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// A granted identity owns both the immutable descriptor and its physical store.
// Virtual partition roots have no store but participate in locks and closure.
type truncateRelation struct {
	ref       ids.ObjectRef
	relation  *catalog.Relation
	storage   *storage.Relation
	privilege bool
}

func checkTruncate(auth AuthorizationView, ref ids.ObjectRef, d *catalog.Relation, privilege, only bool) error {
	if guardedCatalogRelation(d) {
		return guardedCatalogMutationError(auth, d, true)
	}
	if d.RelationKind == catalog.ForeignRelation {
		return sqlError("0A000", "foreign table truncation is unsupported in embedded mode")
	}
	if d.RelationKind != catalog.TableRelation && d.RelationKind != catalog.PartitionedRelation {
		return sqlError("42809", fmt.Sprintf("%q is not a table", d.Name()))
	}
	if privilege {
		if err := auth.Check(access.Requirement{Object: ref, Privileges: authorization.TruncatePrivilege}); err != nil {
			return err
		}
	}
	if only && d.Partition != nil {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: "cannot truncate only a partitioned table", Hint: "Do not specify the ONLY keyword, or use TRUNCATE ONLY on the partitions directly."}
	}
	return nil
}

// Descendant admission and its final graph fence share the inherited-query
// consumer's namespace/session-token authority, not child ACL or USAGE checks.
func truncateOtherTemporary(catalog catalog.Read, relation *catalog.Relation, token ids.SessionToken) (bool, error) {
	namespace, ok := catalog.Namespace(relation.Namespace())
	if !ok {
		return false, ids.ErrCatalogReplaced
	}
	return namespace.Temporary != (ids.SessionToken{}) && namespace.Temporary != token, nil
}

// Explicit targets are opened in user order. Named occurrences always check
// their own ACL, even when an earlier inheritance expansion already selected them.
func (s *StatementScope) acquireTruncate(stmt *parser.Truncate) error {
	e := s.op.session.Engine()
	if !s.op.commandAdmission {
		s.op.snapshotNoticeThreshold()
	}
	selected := make(map[ids.ObjectAddress]int)
	s.truncateSelected = selected
	s.truncateRestart = stmt.RestartIdentity
	expanded := make(map[ids.ObjectAddress]bool)
	var open func(ids.ObjectRef, *truncateName, bool, bool, bool, bool) error
	open = func(ref ids.ObjectRef, name *truncateName, privilege, only, recurse, cascade bool) error {
		e.mu.Lock()
		auth, err := e.statementAuthorizationLocked(s.op, stmt)
		var before *catalog.Relation
		var store *storage.Relation
		otherTemporary := false
		if err == nil {
			if ref.Domain() != e.catalog.Domain || ref.Epoch != e.catalog.state.Epoch() || s.txRef.Epoch() != ref.Epoch {
				err = ids.ErrCatalogReplaced
			} else {
				before, _ = auth.Catalog.Relation(ref.Address)
				if before == nil {
					err = ids.ErrCatalogReplaced
				} else {
					if !privilege {
						otherTemporary, err = truncateOtherTemporary(auth.Catalog, before, s.op.session.token)
					}
					if err == nil && !otherTemporary {
						err = checkTruncate(auth, ref, before, privilege && !cascade, only)
						if err == nil && privilege {
							err = checkTemporaryStorageAccess(auth.Catalog, before, s.op.session.token)
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
		if before.Membership != nil {
			if err = s.acquirePartitionAncestors(ref); err != nil {
				return err
			}
		}
		policy := lock.Policy{TimeoutSlot: &s.op.lockWait, Timeout: s.op.lockTimeout, StatementDeadline: s.op.statementDeadline, Source: s.op.sourceContext}
		request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, lock.Resource{ObjectRef: ref}, lock.AccessExclusive, policy)
		waited := request != nil
		if waited {
			if s.replacementRead {
				e.replacementMu.RUnlock()
				s.replacementRead = false
			}
			err = request.Wait()
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
		var d *catalog.Relation
		if err == nil {
			d, _ = auth.Catalog.Relation(ref.Address)
			current := e.privateStorageLocked(s.owner.catalog, ref.Address)
			if ref.Epoch != e.catalog.state.Epoch() || s.txRef.Epoch() != ref.Epoch || d == nil || before.Namespace() != d.Namespace() || !sameRelationRowShape(before, d) || store != current && !store.SameTruncateLineage(current) {
				err = ids.ErrCatalogReplaced
			} else if name != nil {
				err = e.truncateNameCurrent(auth, *name)
			}
			if err == nil && !privilege {
				otherTemporary, err = truncateOtherTemporary(auth.Catalog, d, s.op.session.token)
				if err == nil && otherTemporary {
					err = ids.ErrCatalogReplaced
				}
			}
			if err == nil && cascade {
				// Records are detached to the adapter later; this invokes no user callback.
				s.op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf("truncate cascades to table %q", d.Name())})
			}
			if err == nil {
				err = checkTruncate(auth, ref, d, privilege, only)
			}
			if err == nil {
				err = s.relationCursorPreflight(auth.Catalog, ref.Address, "TRUNCATE")
			}
			if err == nil {
				if namespace, ok := auth.Catalog.Namespace(d.Namespace()); ok && namespace.Temporary == s.op.session.token {
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
		var children []*catalog.Relation
		if err == nil && recurse && !expanded[ref.Address] {
			expanded[ref.Address] = true
			children = inheritanceChildren(auth.Catalog, ref.Address)
			if d.Partition != nil {
				for _, address := range d.Partition.Children {
					child, ok := auth.Catalog.Relation(address)
					if !ok {
						err = ids.ErrCatalogReplaced
						break
					}
					children = append(children, child)
				}
				slices.SortFunc(children, func(a, b *catalog.Relation) int { return compareTruncateAddress(a.Address(), b.Address()) })
			}
		}
		e.mu.Unlock()
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
		e.mu.Lock()
		auth, err := e.statementAuthorizationLocked(s.op, stmt)
		var ref ids.ObjectRef
		observation := truncateName{name: target.Name}
		if err == nil && s.txRef.Epoch() != e.catalog.state.Epoch() {
			err = ids.ErrCatalogReplaced
		}
		if err == nil {
			ref, err = e.resolveRelationReference(auth, target.Name, true)
		}
		if err == nil {
			descriptor, ok := auth.Catalog.Relation(ref.Address)
			if !ok {
				err = ids.ErrCatalogReplaced
			} else {
				observation.ref, observation.namespace = ref, descriptor.Namespace()
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
			return truncateForeignKeyError(inbound[0].parent.Name(), inbound[0].relation.Name())
		}
		for _, candidate := range inbound {
			ref := e.catalog.Domain.Ref(s.txRef.Epoch(), candidate.relation.Address())
			if err := open(ref, nil, true, false, false, true); err != nil {
				return err
			}
		}
	}
}

type truncateReferencer struct{ relation, parent *catalog.Relation }

func compareTruncateAddress(a, b ids.ObjectAddress) int {
	if a.ObjectID < b.ObjectID {
		return -1
	}
	if a.ObjectID > b.ObjectID {
		return 1
	}
	return 0
}

// FK definitions alone decide closure, including empty referencing tables.
func truncateReferencers(view catalog.Read, selected map[ids.ObjectAddress]int) []truncateReferencer {
	var result []truncateReferencer
	for address, descriptor := range view.Descriptors() {
		d, ok := descriptor.(*catalog.Relation)
		if !ok || selected[address] != 0 {
			continue
		}
		for _, fk := range d.Constraints.ForeignKeys {
			if selected[fk.Target] != 0 {
				parent, _ := view.Relation(fk.Target)
				if parent != nil {
					result = append(result, truncateReferencer{d, parent})
				}
				break
			}
		}
	}
	slices.SortFunc(result, func(a, b truncateReferencer) int {
		return compareTruncateAddress(a.relation.Address(), b.relation.Address())
	})
	return result
}
func truncateForeignKeyError(parent, child string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot truncate a table referenced in a foreign key constraint", Detail: fmt.Sprintf("Table %q references %q.", child, parent), Hint: fmt.Sprintf("Truncate table %q at the same time, or use TRUNCATE ... CASCADE.", child)}
}
