package engine

import (
	"errors"
	"fmt"
	"strconv"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Resolve original authority before waiting; never resolve a replacement by name.
func (s *StatementScope) acquireRelation(stmt parser.Statement, execute bool) (err error) {
	if foreign, ok := stmt.(*parser.ForeignDDL); ok {
		if foreign.Kind == "table" && foreign.Action == "alter" {
			stmt = foreignAlterAdmission(foreign)
		} else {
			return nil
		}
	}
	if view, ok := stmt.(*parser.CreateView); ok && parser.HasModifyingCTE(view.Query) {
		if !execute {
			return nil
		}
		if err := s.acquireCTEMutations(stmt, execute); err != nil {
			return err
		}
		return s.prepareViewCreation(view)
	}
	if parser.HasModifyingCTE(statementCTERoot(stmt)) {
		return s.acquireCTEMutations(stmt, execute)
	}
	if merge, ok := stmt.(*parser.Merge); ok {
		if err := validateMergeReachability(merge); err != nil {
			return err
		}
	}
	switch stmt.(type) {
	case *parser.Truncate:
		if !execute {
			return nil
		}
		return s.acquireTruncate(stmt.(*parser.Truncate))
	case *parser.RefreshMaterializedView:
		if !execute {
			return nil
		}
		return s.acquireMaterializedRefresh(stmt.(*parser.RefreshMaterializedView))
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
		if err == nil && s.admissionWaited {
			e := s.op.session.Engine()
			e.mu.Lock()
			auth, authErr := e.statementAuthorizationLocked(s.op, stmt)
			err = authErr
			if err == nil {
				err = s.validateAdmissionNamesLocked(e, auth)
			}
			e.mu.Unlock()
		}
	}()
	if creation, ok := stmt.(*parser.CreateTable); ok && execute {
		if err := s.acquireForeignKeyCreation(creation); err != nil {
			return err
		}
	}
	if alteration, ok := stmt.(*parser.AlterTable); ok && execute {
		if err := s.acquireAlterForeignKeys(alteration); err != nil {
			return err
		}
	}
	if view, ok := stmt.(*parser.CreateView); ok {
		if !execute {
			return nil
		}
		if err := s.acquireQueryRelations(view.Query, stmt, execute); err != nil {
			return err
		}
		if err := s.prepareViewCreation(view); err != nil {
			return err
		}
		if view.Replace {
			return s.acquireRelationTarget(view, stmt, execute, false)
		}
		return nil
	}
	if creation, ok := stmt.(*parser.CreateQueryTable); ok {
		return s.acquireQueryRelations(creation.Query, stmt, execute)
	}
	if declaration, ok := stmt.(*parser.DeclareCursor); ok {
		return s.acquireQueryRelations(declaration.Query, declaration.Query, execute)
	}
	if sel, ok := stmt.(*parser.Select); ok {
		return s.acquireQueryRelations(sel, stmt, execute)
	}
	if copy, ok := stmt.(*parser.Copy); ok && copy.Query != nil {
		return s.acquireQueryRelations(copy.Query, copy.Query, execute)
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
	case *parser.CreatePolicy, *parser.AlterPolicy:
		if !execute {
			return nil
		}
		if err := s.acquireRelationTarget(stmt, stmt, execute, false); err != nil {
			return err
		}
		return s.acquireQueryRelations(stmt, stmt, execute)
	case *parser.Update, *parser.Delete, *parser.Merge:
		if err := s.acquireRelationTarget(stmt, stmt, execute, false); err != nil {
			return err
		}
		if err := s.acquireInheritedMutationTargets(stmt); err != nil {
			return err
		}
		return s.acquireQueryRelations(stmt, stmt, execute)
	}
	return s.acquireRelationTarget(stmt, stmt, execute, false)
}

func (s *StatementScope) acquireRelationTarget(stmt, authorityStmt parser.Statement, execute, source bool) error {
	var name parser.ObjectName
	var mode lock.Mode
	ownerOnly := false
	rename, ifExists, tableRename := false, false, false
	viewReplacement := false
	policy := lock.Policy{TimeoutSlot: &s.op.lockWait, Timeout: s.op.lockTimeout, StatementDeadline: s.op.statementDeadline, Source: s.op.sourceContext}
	switch v := stmt.(type) {
	case *parser.CreateRule:
		if !execute {
			return nil
		}
		name, mode, ownerOnly, rename = v.Table, lock.AccessExclusive, true, true
	case *parser.AlterRule:
		if !execute {
			return nil
		}
		name, mode, ownerOnly, rename = v.Table, lock.AccessExclusive, true, true
	case *parser.DropRule:
		if !execute {
			return nil
		}
		name, mode, rename, ifExists = v.Table, lock.AccessExclusive, true, v.IfExists
	case *parser.CreatePolicy:
		if !execute {
			return nil
		}
		name, mode, ownerOnly, rename = v.Table, lock.AccessExclusive, true, true
	case *parser.AlterPolicy:
		if !execute {
			return nil
		}
		name, mode, ownerOnly, rename = v.Table, lock.AccessExclusive, true, true
	case *parser.DropPolicy:
		if !execute {
			return nil
		}
		name, mode, rename, ifExists = v.Table, lock.AccessExclusive, true, v.IfExists
	case *parser.CreateTable:
		if !execute || v.PartitionOf == nil {
			return nil
		}
		name, mode, ownerOnly = v.PartitionOf.Name, lock.AccessExclusive, true
	case *parser.CreateIndex:
		if !execute {
			return nil
		}
		name, mode, ownerOnly = v.Table, lock.Share, true
		if v.Concurrent.Character != 0 {
			mode = lock.ShareUpdateExclusive
		}
	case *parser.CreateView:
		if !execute || !v.Replace {
			return nil
		}
		name, mode, ownerOnly = v.Name, lock.AccessExclusive, true
		viewReplacement = true
		rename = true
	case *parser.AlterOwner:
		if !execute || v.Kind == "type" || v.Kind == "domain" || v.Kind == "database" {
			return nil
		}
		name, mode, ownerOnly = v.Name, lock.AccessExclusive, true
		rename, ifExists = true, v.IfExists
	case *parser.AlterView:
		if !execute {
			return nil
		}
		name, mode, ownerOnly = v.Name, lock.AccessExclusive, true
		rename, ifExists, tableRename = true, v.IfExists, v.Action == "rename"
	case *parser.Select:
		if v.From == nil {
			return nil
		}
		name = v.From.(*parser.TableReference).Name
		mode = lock.AccessShare
		if v.Locking != nil {
			mode = lock.RowShare
		}
	case *parser.Copy:
		name, mode = v.Name, lock.AccessShare
		if v.From {
			mode = lock.RowExclusive
		}
	case *parser.Insert:
		name = v.Name
		mode = lock.RowExclusive
	case *parser.Merge:
		name = v.Name
		mode = lock.RowExclusive
	case *parser.Delete:
		name = v.Name
		mode = lock.RowExclusive
	case *parser.Update:
		name = v.Name
		mode = lock.RowExclusive
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
		mode = lock.ShareUpdateExclusive
	case *parser.AlterTable:
		if !execute {
			return nil
		}
		name = v.Name
		mode = alterTableLockMode(v)
		ownerOnly = true
		rename, ifExists = true, v.IfExists
	case *parser.AlterTableAttachPartition:
		if !execute {
			return nil
		}
		name, mode, ownerOnly = v.Name, lock.ShareUpdateExclusive, true
		rename, ifExists = true, v.IfExists
	case *parser.AlterTableDetachPartition:
		if !execute {
			return nil
		}
		name, mode, ownerOnly = v.Name, lock.AccessExclusive, true
		rename, ifExists = true, v.IfExists
	case *parser.AlterTableOf:
		if !execute {
			return nil
		}
		name, mode, ownerOnly = v.Name, lock.AccessExclusive, true
	case *parser.AlterTableDefault:
		if !execute {
			return nil
		}
		name, mode, ownerOnly = v.Name, lock.AccessExclusive, true
	case *parser.AlterTableRenameConstraint:
		if !execute {
			return nil
		}
		name, mode, ownerOnly = v.Name, lock.AccessExclusive, true
		rename, ifExists = true, v.IfExists
	case *parser.AlterTableRenameColumn:
		if !execute {
			return nil
		}
		name, mode, ownerOnly = v.Name, lock.AccessExclusive, true
		rename, ifExists = true, v.IfExists
	case *parser.AlterTableSetSchema:
		if !execute {
			return nil
		}
		name, mode, ownerOnly = v.Name, lock.AccessExclusive, true
		rename, ifExists = true, v.IfExists
	case *parser.AlterTableRenameTable:
		if !execute {
			return nil
		}
		name, mode, ownerOnly = v.Name, lock.AccessExclusive, true
		rename, ifExists, tableRename = true, v.IfExists, true
	default:
		return nil
	}
	e := s.op.session.Engine()
	e.mu.Lock()
	if s.txRef.Epoch() != e.catalog.state.Epoch() {
		e.mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	auth, err := e.statementAuthorizationLocked(s.op, authorityStmt)
	parentAuth := auth
	if err == nil {
		err = policyPreflight(s.op, auth, stmt, true)
	}
	if _, attaching := stmt.(*parser.AlterTableAttachPartition); attaching && err == nil {
		parentAuth, err = e.partitionAttachAuthorization(s.op, auth)
	}
	if creation, ok := stmt.(*parser.CreateTable); ok && err == nil {
		parentAuth, err = e.partitionParentAuthorization(s.op, auth, creation)
		if err == nil {
			err = s.checkPartitionCreationNamespaceLocked(auth, creation)
		}
	}
	var ref ids.ObjectRef
	retained := false
	if err == nil {
		if selection, ok := stmt.(*parser.Select); source && ok {
			if table, ok := selection.From.(*parser.TableReference); ok {
				ref = s.queryLockAdmission.tableQueries[table].ref
			}
		} else if s.preparedReuse != nil {
			switch stmt.(type) {
			case *parser.Insert, *parser.Update, *parser.Delete, *parser.Merge:
				if s.preparedReuse.targets != nil {
					ref = s.preparedReuse.targets[stmt]
				} else {
					ref = s.preparedReuse.target
				}
			}
		}
		if ref.Domain() != nil {
			retained = true
			ref, err = e.admittedReference(auth, name, ref)
		} else if viewReplacement {
			// CREATE targets only its creation namespace, not a same-named
			// relation later in the lookup path. Qualified creation needs
			// CREATE, not USAGE; body lookup retains its independent authority.
			ns := s.viewCreationNamespace
			if s.viewChanges != nil {
				auth.catalog = e.privateCatalogLocked(s.viewChanges)
				auth.recaptureCatalog(auth.catalog)
			}
			err = auth.CheckCreate(ns)
			if err == nil {
				s.viewCreationNamespace = ns
				address := auth.catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns.Address, Name: name.Parts[len(name.Parts)-1].Text})
				if address != (ids.ObjectAddress{}) {
					ref = e.catalog.Domain.Ref(auth.catalog.Epoch(), address)
				}
			}
		} else if mode == lock.AccessShare || mode == lock.RowShare || mode == lock.RowExclusive {
			if _, copyTarget := stmt.(*parser.Copy); copyTarget {
				ref, err = e.resolveRelationReference(auth, name, true)
			} else {
				ref, err = e.resolveReference(auth, name)
			}
		} else {
			_, indexBuild := stmt.(*parser.CreateIndex)
			_, partitionCreate := stmt.(*parser.CreateTable)
			ref, err = e.resolveRelationReference(parentAuth, name, mode == lock.ShareUpdateExclusive || rename || indexBuild || partitionCreate)
		}
	}
	if rename && ifExists && renameMissing(err) {
		err = nil
	}
	if err == nil {
		switch utility := stmt.(type) {
		case *parser.CreateView:
			if ref.Domain() != nil {
				err = auth.Check(AccessRequirement{Object: ref, OwnerOnly: true})
				if err == nil {
					d := auth.catalog.MustRelation(ref.Address)
					if d.RelationKind != viewRelation {
						err = sqlError("42809", fmt.Sprintf(`"%s" is not a view`, d.Name()))
					}
				}
			}
		case *parser.AlterView:
			if ref.Domain() != nil {
				err = alterViewAuthority(parentAuth, ref, utility)
			}
		}
		if ownerOnly && !viewReplacement && err == nil {
			if ref.Domain() != nil {
				err = parentAuth.Check(AccessRequirement{Object: ref, OwnerOnly: true, OwnerNoun: policyOwnerNoun(stmt)})
				if _, altering := stmt.(*parser.AlterTable); altering && err == nil {
					err = alterTableLookupKind(parentAuth.catalog.MustRelation(ref.Address))
				}
			}
			if tableRename && ref.Domain() != nil && err == nil {
				err = auth.CheckCreate(e.catalog.Domain.Ref(ref.Epoch, auth.catalog.Get(ref.Address).Namespace()))
			}
		}
		if err == nil && ref.Domain() != nil {
			err = partitionConsumerBoundary(s.op, auth, stmt, auth.catalog.MustRelation(ref.Address))
		}
	}
	var definitionStamp ids.DefinitionRevision
	var materializedAlterAuthority *relationDescriptor
	if err == nil && ref.Domain() != nil {
		definitionStamp = preparedDefinitionStamp(auth.catalog.Get(ref.Address), s.op.session)
		if utility, ok := stmt.(*parser.AlterView); ok && utility.Materialized {
			materializedAlterAuthority = auth.catalog.MustRelation(ref.Address)
		}
		_, attaching := stmt.(*parser.AlterTableAttachPartition)
		_, detaching := stmt.(*parser.AlterTableDetachPartition)
		if source || preparableBody(stmt) || attaching || detaching {
			lexical := ref
			if retained {
				lexical, err = e.admissionNameReference(auth, name)
			}
			if source {
				table := stmt.(*parser.Select).From.(*parser.TableReference)
				entry := s.queryLockAdmission.tableQueries[table]
				entry.lexical = lexical
				s.queryLockAdmission.tableQueries[table] = entry
			} else {
				s.admittedTargetName, s.admittedTargetLexical = name, lexical
			}
		}
	}
	var typedSourceRef ids.ObjectRef
	if attachment, ok := stmt.(*parser.AlterTableOf); ok && attachment.Type != nil && err == nil {
		var typ *typeDescriptor
		typ, err = e.namedType(auth, *attachment.Type)
		if err == nil {
			var source *relationDescriptor
			var path []ids.ObjectAddress
			path, err = e.referencePath(auth)
			if err == nil {
				source, err = standaloneComposite(auth.catalog, typ, path)
			}
			if err == nil {
				typedSourceRef = e.catalog.Domain.Ref(auth.catalog.Epoch(), source.Address())
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
	if rename && ref.Domain() == nil {
		return nil
	}
	if typedSourceRef.Domain() != nil {
		if err := s.acquireSequence(typedSourceRef, lock.AccessShare, false); err != nil {
			return err
		}
	}
	if _, reading := stmt.(*parser.Select); !reading {
		if err = s.acquirePartitionAncestors(ref); err != nil {
			return err
		}
	}
	request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, lock.Resource{ObjectRef: ref}, mode, policy)
	if errors.Is(err, lock.ErrUnavailable) {
		relationName := name.Parts[0].Text
		if len(name.Parts) == 2 {
			relationName += "." + name.Parts[1].Text
		}
		return sqlError("55P03", fmt.Sprintf("could not obtain lock on relation \"%s\"", relationName))
	}
	if request != nil {
		s.admissionWaited = true
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		err = request.Wait()
	}
	if err != nil {
		// Parse analysis opens SELECT/INSERT/UPDATE/DELETE with a positioned
		// error callback. Cached execution, COPY targets and utility locks do not.
		// Query cancellation (57014) deliberately bypasses that callback.
		_, copyTarget := stmt.(*parser.Copy)
		if !copyTarget && (mode == lock.AccessShare || mode == lock.RowShare || mode == lock.RowExclusive) && (!execute || s.Expected == nil) {
			var diagnostic *sqlstate.Error
			if errors.As(err, &diagnostic) && (diagnostic.Code == "55P03" || diagnostic.Code == "40P01" || diagnostic.Code == "25P04") && name.Parts[0].Pos.Character > 0 {
				copy := *diagnostic
				copy.Position = strconv.Itoa(name.Parts[0].Pos.Character)
				return &copy
			}
		}
		return err
	}
	partitionDrop := false
	e.mu.Lock()
	if ref.Domain() != nil {
		err = s.observePartitionRelationLocked(e, ref)
		if action, ok := stmt.(*parser.AlterTable); ok && err == nil {
			d, exists := e.privateCatalogLocked(s.owner.catalog).Relation(ref.Address)
			partitionDrop = exists && hierarchyRelation(d) && partitionConstraintDropActions(d, action.Actions)
		}
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	switch creation := stmt.(type) {
	case *parser.CreateTable:
		if !creation.PartitionOf.Bound.Default {
			if err = s.acquirePartitionDefinitionDefault(ref); err != nil {
				return err
			}
		}
	case *parser.AlterTable:
		if partitionDrop {
			if err = s.acquirePartitionDescendants(ref, lock.AccessExclusive); err != nil {
				return err
			}
		}
	case *parser.CreateIndex:
		if err = s.acquirePartitionDescendants(ref, lock.Share); err != nil {
			return err
		}
	case *parser.Select, *parser.Insert:
		if err = s.acquirePartitionDescendants(ref, lock.AccessShare); err != nil {
			return err
		}
	case *parser.Copy:
		if creation.From {
			if err = s.acquirePartitionDescendants(ref, lock.AccessShare); err != nil {
				return err
			}
		}
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
	descriptor, live := e.privateCatalogLocked(s.owner.catalog).Relation(ref.Address)
	if ref.Domain() != e.catalog.Domain || ref.Epoch != e.catalog.state.Epoch() || txref.Epoch() != e.catalog.state.Epoch() || !live {
		return ids.ErrCatalogReplaced
	}
	if materializedAlterAuthority != nil &&
		(descriptor.RelationKind != materializedAlterAuthority.RelationKind ||
			!relationDefiningAuthorityCompatible(materializedAlterAuthority, descriptor) ||
			descriptor.SQLIndex != materializedAlterAuthority.SQLIndex) {
		// Check current ownership on the admitted identity, not its old name.
		// A still-authorized actor cannot adopt a changed defining predecessor.
		currentAuth, authorityErr := e.statementAuthorizationLocked(s.op, authorityStmt)
		if authorityErr != nil {
			return authorityErr
		}
		if authorityErr = alterViewAuthority(currentAuth, ref, stmt.(*parser.AlterView)); authorityErr != nil {
			return authorityErr
		}
		return ids.ErrCatalogReplaced
	}
	if preparedDefinitionStamp(descriptor, s.op.session) != definitionStamp {
		s.preparedChangedDuringAdmission = true
	}
	if source {
		s.querySources = append(s.querySources, queryLeaf{ref: ref, relation: descriptor, only: sourceOnly(stmt), storage: e.privateStorageLocked(s.owner.catalog, ref.Address)})
	} else {
		s.lockedRef = ref
		s.lockedDescriptor = descriptor
		storageTarget := false
		switch statement := stmt.(type) {
		case *parser.Copy:
			storageTarget = descriptor.RelationKind == tableRelation || descriptor.RelationKind == materializedViewRelation
		case *parser.Delete, *parser.Update, *parser.Merge:
			storageTarget = descriptor.RelationKind == tableRelation
		case *parser.Insert:
			storageTarget = descriptor.RelationKind == tableRelation && (len(s.mutations) != 0 || statement.Conflict != nil && statement.Conflict.Update)
		}
		if storageTarget {
			s.lockedStorage = e.privateStorageLocked(s.owner.catalog, ref.Address)
			if s.lockedStorage == nil {
				return errors.New("stuntgres: relation storage is missing")
			}
		}
		s.lockedIdentityOnly = mode == lock.ShareUpdateExclusive || mode == lock.Share
	}
	// Definition evolution while waiting is valid for this exact relation.
	// Pin the grant-time descriptor and revalidate current role authority.
	auth, err = e.statementAuthorizationLocked(s.op, authorityStmt)
	if err != nil {
		return err
	}
	if viewReplacement {
		ns := s.viewCreationNamespace
		if s.viewChanges != nil {
			auth.catalog = e.privateCatalogLocked(s.viewChanges)
			auth.recaptureCatalog(auth.catalog)
		}
		if auth.catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns.Address, Name: name.Parts[len(name.Parts)-1].Text}) != ref.Address {
			return ids.ErrCatalogReplaced
		}
		if err := auth.CheckCreate(ns); err != nil {
			return err
		}
		if err := auth.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
			return err
		}
		if descriptor.RelationKind != viewRelation {
			return sqlError("42809", fmt.Sprintf(`"%s" is not a view`, descriptor.Name()))
		}
		return nil
	}
	if ownerOnly {
		parentAuth = auth
		if creation, ok := stmt.(*parser.CreateTable); ok {
			parentAuth, err = e.partitionParentAuthorization(s.op, auth, creation)
			if err != nil {
				return err
			}
			if err := s.checkPartitionCreationNamespaceLocked(auth, creation); err != nil {
				return err
			}
		}
		if _, attaching := stmt.(*parser.AlterTableAttachPartition); attaching {
			parentAuth, err = e.partitionAttachAuthorization(s.op, auth)
			if err != nil {
				return err
			}
		}
		if err := parentAuth.Check(AccessRequirement{Object: ref, OwnerOnly: true, OwnerNoun: policyOwnerNoun(stmt)}); err != nil {
			return err
		}
		if _, altering := stmt.(*parser.AlterTable); altering {
			if err := alterTableLookupKind(descriptor); err != nil {
				return err
			}
		}
		if tableRename {
			return auth.CheckCreate(e.catalog.Domain.Ref(ref.Epoch, descriptor.Namespace()))
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
	if len(s.mutations) != 0 {
		active := s.activeMutation
		defer s.activateMutation(active)
		for i := range s.mutations {
			s.activateMutation(i)
			if err := s.validateActiveMutationLocked(e); err != nil {
				return err
			}
		}
		s.activateMutation(active)
	}
	if len(s.querySources) == 0 && s.lockedRef.Domain() == nil && len(s.partitionRelations) == 0 {
		return nil
	}
	view := e.privateCatalogLocked(s.owner.catalog)
	for _, source := range s.inheritanceRelations {
		if _, err := currentQuerySourceLocked(e, view, s.owner.catalog, source); err != nil {
			return err
		}
	}
	for i, source := range s.querySources {
		current, err := currentQuerySourceLocked(e, view, s.owner.catalog, source)
		if err != nil {
			return err
		}
		if refreshSources && (current.RelationKind != materializedViewRelation || s.view == nil) {
			s.querySources[i].relation = current
			if current.RelationKind == materializedViewRelation {
				s.querySources[i].storage = e.privateStorageLocked(s.owner.catalog, source.ref.Address)
			}
		}
	}
	if len(s.mutations) != 0 {
		return nil
	}
	return s.validateActiveMutationLocked(e)
}

func (s *StatementScope) validateActiveMutationLocked(e *Engine) error {
	if s.lockedRef.Domain() == nil {
		return nil
	}
	view := e.privateCatalogLocked(s.owner.catalog)
	if s.lockedRef.Domain() != e.catalog.Domain || s.lockedRef.Epoch != e.catalog.state.Epoch() {
		return ids.ErrCatalogReplaced
	}
	current := view.Get(s.lockedRef.Address)
	if s.lockedIdentityOnly {
		if _, ok := current.(*relationDescriptor); !ok {
			return ids.ErrCatalogReplaced
		}
	} else if s.lockedStorage != nil {
		relation, ok := current.(*relationDescriptor)
		captured, capturedOK := s.lockedDescriptor.(*relationDescriptor)
		if !ok || !capturedOK || s.lockedRef.Address.SubID != 0 || e.privateStorageLocked(s.owner.catalog, s.lockedRef.Address) != s.lockedStorage || !sameRelationRowShape(captured, relation) {
			return ids.ErrCatalogReplaced
		}
	} else if current != s.lockedDescriptor {
		return ids.ErrCatalogReplaced
	}
	for _, target := range s.inheritedTargets {
		if _, err := currentQuerySourceLocked(e, view, s.owner.catalog, target); err != nil {
			return err
		}
	}
	for address, captured := range s.partitionRelations {
		current, _ := view.Relation(address)
		if current != captured && !partitionAppendCompatible(captured, current) {
			return ids.ErrCatalogReplaced
		}
	}
	if s.viewWrite != nil {
		for _, layer := range s.viewWrite.layers {
			current, ok := view.Relation(layer.ref.Address)
			if !ok || current.View != layer.relation.View || !sameRelationRowShape(current, layer.relation) {
				return ids.ErrCatalogReplaced
			}
		}
	}
	return nil
}

func sameRelationRowShape(a, b *relationDescriptor) bool {
	if a == b {
		return true
	}
	if len(a.Columns) != len(b.Columns) || len(a.Attributes) != len(a.Columns) || len(b.Attributes) != len(b.Columns) {
		return false
	}
	for i, column := range a.Columns {
		other := b.Columns[i]
		if a.Attributes[i] != b.Attributes[i] || column.Name != other.Name || !sameStatementUsage(column.Usage, other.Usage) || column.Nullable != other.Nullable || column.NullabilityKnown != other.NullabilityKnown {
			return false
		}
	}
	return true
}

// Called under Engine.mu after partitionParentAuthorization validates any
// trusted importer provenance. Destination rights belong to the named child
// principal, never the parent's private restore authorization.
func (s *StatementScope) checkPartitionCreationNamespaceLocked(auth AuthorizationView, creation *parser.CreateTable) error {
	if creation.Persistence == "temporary" {
		return nil
	}
	ns, deferredTemp, err := s.op.session.Engine().creationNamespaceDestination(auth, creation.Name, false)
	if err == nil && !deferredTemp && !s.op.restoreTableDestination {
		err = auth.CheckCreate(ns)
	}
	if err != nil {
		return creationError(err, creation.Name)
	}
	return nil
}
