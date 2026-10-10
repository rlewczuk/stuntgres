package engine

import (
	"errors"
	"fmt"
	"strconv"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Resolve original authority before waiting; never resolve a replacement by name.
func (s *StatementScope) acquireRelation(stmt parser.Statement, execute bool) (err error) {
	switch stmt.(type) {
	case *parser.DropIndex, *parser.AlterIndex, *parser.Reindex:
		if !execute {
			return nil
		}
		return s.acquireIndexMaintenance(stmt)
	}
	if lock, ok := stmt.(*parser.LockTable); ok {
		if !execute {
			return nil
		}
		return s.acquireExplicitLocks(lock)
	}
	defer func() {
		if err == nil {
			err = s.acquireViewSources()
		}
	}()
	if view, ok := stmt.(*parser.CreateView); ok {
		if execute && view.Replace {
			if err := s.acquireRelationTarget(view, stmt, execute, false); err != nil {
				return err
			}
		}
		return s.acquireQueryRelations(view.Query, stmt, execute)
	}
	if creation, ok := stmt.(*parser.CreateQueryTable); ok {
		return s.acquireQueryRelations(creation.Query, stmt, execute)
	}
	if sel, ok := stmt.(*parser.Select); ok {
		return s.acquireQueryRelations(sel, stmt, execute)
	}
	if insert, ok := stmt.(*parser.Insert); ok {
		if err := s.acquireRelationTarget(stmt, stmt, execute, false); err != nil {
			return err
		}
		// PostgreSQL validates explicit target identifiers before opening the
		// source, so a blocked/missing source cannot mask a bad target list.
		if err := bindInsertTargets(s.lockedDescriptor.(*relationDescriptor), insert.Targets, nil); err != nil {
			return err
		}
		return s.acquireQueryRelations(stmt, stmt, execute)
	}
	switch stmt.(type) {
	case *parser.Update, *parser.Delete:
		if err := s.acquireRelationTarget(stmt, stmt, execute, false); err != nil {
			return err
		}
		return s.acquireQueryRelations(stmt, stmt, execute)
	}
	return s.acquireRelationTarget(stmt, stmt, execute, false)
}

func (s *StatementScope) acquireRelationTarget(stmt, authorityStmt parser.Statement, execute, source bool) error {
	var name parser.ObjectName
	var mode relationLockMode
	ownerOnly := false
	rename, ifExists, tableRename := false, false, false
	viewReplacement := false
	policy := relationLockPolicy{timeout: s.op.lockTimeout, statementDeadline: s.op.statementDeadline, source: s.op.sourceContext}
	switch v := stmt.(type) {
	case *parser.CreateIndex:
		if !execute {
			return nil
		}
		name, mode, ownerOnly = v.Table, relationShare, true
		if v.Concurrent.Character != 0 {
			mode = shareUpdateExclusive
		}
	case *parser.CreateView:
		if !execute || !v.Replace {
			return nil
		}
		name, mode, ownerOnly = v.Name, accessExclusive, true
		viewReplacement = true
		rename = true
	case *parser.AlterView:
		if !execute {
			return nil
		}
		name, mode, ownerOnly = v.Name, accessExclusive, true
		rename, ifExists, tableRename = true, v.IfExists, v.Action == "rename"
	case *parser.Select:
		if v.From == nil {
			return nil
		}
		name = v.From.(*parser.TableReference).Name
		mode = accessShare
		if v.Locking != nil {
			mode = relationRowShare
		}
	case *parser.Insert:
		name = v.Name
		mode = rowExclusive
	case *parser.Delete:
		name = v.Name
		mode = rowExclusive
	case *parser.Update:
		name = v.Name
		mode = rowExclusive
	case *parser.Comment:
		if !execute || (v.Kind != parser.CommentTable && v.Kind != parser.CommentColumn) {
			return nil
		}
		name = v.Name
		if v.Kind == parser.CommentColumn {
			if len(name.Parts) < 2 {
				return sqlError("42601", "column name must be qualified")
			}
			name.Parts = name.Parts[:len(name.Parts)-1]
		}
		mode = shareUpdateExclusive
	case *parser.AlterTable:
		if !execute {
			return nil
		}
		name = v.Name
		mode = accessExclusive
		ownerOnly = true
	case *parser.AlterTableDefault:
		if !execute {
			return nil
		}
		name, mode, ownerOnly = v.Name, accessExclusive, true
	case *parser.AlterTableRenameColumn:
		if !execute {
			return nil
		}
		name, mode, ownerOnly = v.Name, accessExclusive, true
		rename, ifExists = true, v.IfExists
	case *parser.AlterTableSetSchema:
		if !execute {
			return nil
		}
		name, mode, ownerOnly = v.Name, accessExclusive, true
		rename, ifExists = true, v.IfExists
	case *parser.AlterTableRenameTable:
		if !execute {
			return nil
		}
		name, mode, ownerOnly = v.Name, accessExclusive, true
		rename, ifExists, tableRename = true, v.IfExists, true
	default:
		return nil
	}
	e := s.op.session.Engine()
	e.mu.Lock()
	if s.txRef.epoch != e.state.epoch {
		e.mu.Unlock()
		return ErrCatalogReplaced
	}
	auth, err := e.statementAuthorizationLocked(s.op, authorityStmt)
	var ref ObjectRef
	if err == nil {
		if viewReplacement {
			// CREATE targets only its creation namespace, not a same-named
			// relation later in the lookup path. Qualified creation needs
			// CREATE, not USAGE; body lookup retains its independent authority.
			var ns ObjectRef
			ns, err = e.creationNamespace(auth, name)
			if err == nil {
				err = checkTableCreationNamespace(auth.catalog, ns.Address)
			}
			if err == nil {
				err = auth.CheckCreate(ns)
			}
			if err != nil {
				err = creationError(err, name)
			} else {
				s.viewCreationNamespace = ns
				address := auth.catalog.state.names[NameKey{ClassID: RelationClassID, Namespace: ns.Address, Name: name.Parts[len(name.Parts)-1].Text}]
				if address != (ObjectAddress{}) {
					ref = ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: address}
				}
			}
		} else if mode == accessShare || mode == relationRowShare || mode == rowExclusive {
			ref, err = e.resolveReference(auth, name)
		} else {
			_, indexBuild := stmt.(*parser.CreateIndex)
			ref, err = e.resolveRelationReference(auth, name, mode == shareUpdateExclusive || rename || indexBuild)
		}
	}
	if rename && ifExists && renameMissing(err) {
		err = nil
	}
	if err == nil {
		switch stmt.(type) {
		case *parser.CreateView:
			if ref.owner != nil {
				err = auth.Check(AccessRequirement{Object: ref, OwnerOnly: true})
				if err == nil {
					d := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
					if d.relationKind != viewRelation {
						err = sqlError("42809", fmt.Sprintf(`"%s" is not a view`, d.name))
					}
				}
			}
		case *parser.AlterView:
			if ref.owner != nil {
				d := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
				if d.relationKind != viewRelation {
					err = sqlError("42809", fmt.Sprintf(`"%s" is not a view`, d.name))
				}
			}
		}
		if ownerOnly && !viewReplacement && err == nil {
			if ref.owner != nil {
				err = auth.Check(AccessRequirement{Object: ref, OwnerOnly: true})
			}
			if tableRename && ref.owner != nil && err == nil {
				err = auth.CheckCreate(ObjectRef{owner: e.owner, Epoch: ref.Epoch, Address: auth.catalog.state.descriptors[ref.Address].Namespace()})
			}
		}
	}
	e.mu.Unlock()
	// Catalog provider validation may call Reset; ALTER must not retain a
	// replacement read admission across validation or publication.
	if ownerOnly && s.replacementRead {
		e.replacementMu.RUnlock()
		s.replacementRead = false
	}
	if err != nil {
		return err
	}
	if rename && ref.owner == nil {
		return nil
	}
	request, err := e.locks.startAcquire(s.op.Context(), s.ticket, lockResource{ObjectRef: ref}, mode, policy)
	if errors.Is(err, errRelationUnavailable) {
		relationName := name.Parts[0].Text
		if len(name.Parts) == 2 {
			relationName += "." + name.Parts[1].Text
		}
		return sqlError("55P03", fmt.Sprintf("could not obtain lock on relation \"%s\"", relationName))
	}
	if request != nil {
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		err = request.wait()
	}
	if err != nil {
		// Parse analysis opens SELECT/INSERT/UPDATE/DELETE with a positioned
		// error callback. Cached execution and utility locks do not. Query
		// cancellation (57014) deliberately bypasses that callback.
		if (mode == accessShare || mode == relationRowShare || mode == rowExclusive) && (!execute || s.Expected == nil) {
			var diagnostic *sqlstate.Error
			if errors.As(err, &diagnostic) && (diagnostic.Code == "55P03" || diagnostic.Code == "40P01") && name.Parts[0].Pos.Character > 0 {
				copy := *diagnostic
				copy.Position = strconv.Itoa(name.Parts[0].Pos.Character)
				return &copy
			}
		}
		return err
	}
	s.owner.mu.Lock()
	implicit := s.owner.state == TxImplicit
	txref := s.owner.ref
	s.owner.mu.Unlock()
	if implicit && !ownerOnly && !s.replacementRead {
		e.replacementMu.RLock()
		s.replacementRead = true
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	descriptor, live := e.privateCatalogLocked(s.owner.catalog).state.descriptors[ref.Address].(*relationDescriptor)
	if ref.owner != e.owner || ref.Epoch != e.state.epoch || txref.epoch != e.state.epoch || !live {
		return ErrCatalogReplaced
	}
	if source {
		s.querySources = append(s.querySources, queryLeaf{ref: ref, relation: descriptor, storage: e.privateStorageLocked(s.owner.catalog, ref.Address)})
	} else {
		s.lockedRef = ref
		s.lockedDescriptor = descriptor
		switch stmt.(type) {
		case *parser.Delete, *parser.Update:
			if descriptor.relationKind != tableRelation {
				break
			}
			s.lockedStorage = e.privateStorageLocked(s.owner.catalog, ref.Address)
			if s.lockedStorage == nil {
				return errors.New("stuntgres: relation storage is missing")
			}
		}
		s.lockedIdentityOnly = mode == shareUpdateExclusive || mode == relationShare
	}
	// Definition evolution while waiting is valid for this exact relation.
	// Pin the grant-time descriptor and revalidate current role authority.
	auth, err = e.statementAuthorizationLocked(s.op, authorityStmt)
	if err != nil {
		return err
	}
	if viewReplacement {
		ns, err := e.creationNamespace(auth, name)
		if err != nil {
			return creationError(err, name)
		}
		if ns != s.viewCreationNamespace || auth.catalog.state.names[NameKey{ClassID: RelationClassID, Namespace: ns.Address, Name: name.Parts[len(name.Parts)-1].Text}] != ref.Address {
			return ErrCatalogReplaced
		}
		if err := auth.CheckCreate(ns); err != nil {
			return creationError(err, name)
		}
		if err := auth.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
			return err
		}
		if descriptor.relationKind != viewRelation {
			return sqlError("42809", fmt.Sprintf(`"%s" is not a view`, descriptor.name))
		}
		return nil
	}
	if ownerOnly {
		if err := auth.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
			return err
		}
		if tableRename {
			return auth.CheckCreate(ObjectRef{owner: e.owner, Epoch: ref.Epoch, Address: descriptor.namespace})
		}
		return nil
	}
	return nil
}

func (s *StatementScope) validateLockedRelation(e *Engine) error {
	return s.validateRelationGrantsLocked(e, false)
}

// Final analysis refreshes compatible source metadata in the same critical
// section as authorization/binding. Later validation never rewrites layouts.
func (s *StatementScope) refreshRelationGrantsLocked(e *Engine) error {
	return s.validateRelationGrantsLocked(e, true)
}

func (s *StatementScope) validateRelationGrantsLocked(e *Engine, refreshSources bool) error {
	if len(s.querySources) == 0 && s.lockedRef.owner == nil {
		return nil
	}
	catalog := e.privateCatalogLocked(s.owner.catalog)
	for i, source := range s.querySources {
		current, err := currentQuerySourceLocked(e, catalog, s.owner.catalog, source)
		if err != nil {
			return err
		}
		if refreshSources {
			s.querySources[i].relation = current
		}
	}
	if s.lockedRef.owner == nil {
		return nil
	}
	if s.lockedRef.owner != e.owner || s.lockedRef.Epoch != e.state.epoch {
		return ErrCatalogReplaced
	}
	current := catalog.state.descriptors[s.lockedRef.Address]
	if s.lockedIdentityOnly {
		if _, ok := current.(*relationDescriptor); !ok {
			return ErrCatalogReplaced
		}
	} else if s.lockedStorage != nil {
		relation, ok := current.(*relationDescriptor)
		captured, capturedOK := s.lockedDescriptor.(*relationDescriptor)
		if !ok || !capturedOK || s.lockedRef.Address.SubID != 0 || e.privateStorageLocked(s.owner.catalog, s.lockedRef.Address) != s.lockedStorage || !sameRelationRowShape(captured, relation) {
			return ErrCatalogReplaced
		}
	} else if current != s.lockedDescriptor {
		return ErrCatalogReplaced
	}
	return nil
}

func sameRelationRowShape(a, b *relationDescriptor) bool {
	if a == b {
		return true
	}
	if len(a.columns) != len(b.columns) || len(a.attributes) != len(a.columns) || len(b.attributes) != len(b.columns) {
		return false
	}
	for i, column := range a.columns {
		other := b.columns[i]
		if a.attributes[i] != b.attributes[i] || column.Name != other.Name || !sameStatementUsage(column.Usage, other.Usage) || column.Nullable != other.Nullable || column.NullabilityKnown != other.NullabilityKnown {
			return false
		}
	}
	return true
}
