package engine

import (
	"errors"
	"fmt"
	"strconv"
	"stuntgres/internal/engine/ids"
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
	var mode relationLockMode
	ownerOnly := false
	rename, ifExists, tableRename := false, false, false
	viewReplacement := false
	policy := relationLockPolicy{operation: s.op, timeout: s.op.lockTimeout, statementDeadline: s.op.statementDeadline, source: s.op.sourceContext}
	switch v := stmt.(type) {
	case *parser.CreateRule:
		if !execute {
			return nil
		}
		name, mode, ownerOnly, rename = v.Table, accessExclusive, true, true
	case *parser.AlterRule:
		if !execute {
			return nil
		}
		name, mode, ownerOnly, rename = v.Table, accessExclusive, true, true
	case *parser.DropRule:
		if !execute {
			return nil
		}
		name, mode, rename, ifExists = v.Table, accessExclusive, true, v.IfExists
	case *parser.CreatePolicy:
		if !execute {
			return nil
		}
		name, mode, ownerOnly, rename = v.Table, accessExclusive, true, true
	case *parser.AlterPolicy:
		if !execute {
			return nil
		}
		name, mode, ownerOnly, rename = v.Table, accessExclusive, true, true
	case *parser.DropPolicy:
		if !execute {
			return nil
		}
		name, mode, rename, ifExists = v.Table, accessExclusive, true, v.IfExists
	case *parser.CreateTable:
		if !execute || v.PartitionOf == nil {
			return nil
		}
		name, mode, ownerOnly = v.PartitionOf.Name, accessExclusive, true
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
	case *parser.AlterOwner:
		if !execute || v.Kind == "type" || v.Kind == "domain" || v.Kind == "database" {
			return nil
		}
		name, mode, ownerOnly = v.Name, accessExclusive, true
		rename, ifExists = true, v.IfExists
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
	case *parser.Copy:
		name, mode = v.Name, accessShare
		if v.From {
			mode = rowExclusive
		}
	case *parser.Insert:
		name = v.Name
		mode = rowExclusive
	case *parser.Merge:
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
		mode = alterTableLockMode(v)
		ownerOnly = true
		rename, ifExists = true, v.IfExists
	case *parser.AlterTableAttachPartition:
		if !execute {
			return nil
		}
		name, mode, ownerOnly = v.Name, shareUpdateExclusive, true
		rename, ifExists = true, v.IfExists
	case *parser.AlterTableDetachPartition:
		if !execute {
			return nil
		}
		name, mode, ownerOnly = v.Name, accessExclusive, true
		rename, ifExists = true, v.IfExists
	case *parser.AlterTableOf:
		if !execute {
			return nil
		}
		name, mode, ownerOnly = v.Name, accessExclusive, true
	case *parser.AlterTableDefault:
		if !execute {
			return nil
		}
		name, mode, ownerOnly = v.Name, accessExclusive, true
	case *parser.AlterTableRenameConstraint:
		if !execute {
			return nil
		}
		name, mode, ownerOnly = v.Name, accessExclusive, true
		rename, ifExists = true, v.IfExists
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
	if s.txRef.Epoch() != e.state.epoch {
		e.mu.Unlock()
		return ErrCatalogReplaced
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
				address := auth.catalog.state.names[ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns.Address, Name: name.Parts[len(name.Parts)-1].Text}]
				if address != (ids.ObjectAddress{}) {
					ref = e.domain.Ref(auth.catalog.Epoch(), address)
				}
			}
		} else if mode == accessShare || mode == relationRowShare || mode == rowExclusive {
			if _, copyTarget := stmt.(*parser.Copy); copyTarget {
				ref, err = e.resolveRelationReference(auth, name, true)
			} else {
				ref, err = e.resolveReference(auth, name)
			}
		} else {
			_, indexBuild := stmt.(*parser.CreateIndex)
			_, partitionCreate := stmt.(*parser.CreateTable)
			ref, err = e.resolveRelationReference(parentAuth, name, mode == shareUpdateExclusive || rename || indexBuild || partitionCreate)
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
					d := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
					if d.relationKind != viewRelation {
						err = sqlError("42809", fmt.Sprintf(`"%s" is not a view`, d.name))
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
					err = alterTableLookupKind(parentAuth.catalog.state.descriptors[ref.Address].(*relationDescriptor))
				}
			}
			if tableRename && ref.Domain() != nil && err == nil {
				err = auth.CheckCreate(e.domain.Ref(ref.Epoch, auth.catalog.state.descriptors[ref.Address].Namespace()))
			}
		}
		if err == nil && ref.Domain() != nil {
			err = partitionConsumerBoundary(s.op, auth, stmt, auth.catalog.state.descriptors[ref.Address].(*relationDescriptor))
		}
	}
	var definitionStamp ids.DefinitionRevision
	var materializedAlterAuthority *relationDescriptor
	if err == nil && ref.Domain() != nil {
		definitionStamp = preparedDefinitionStamp(auth.catalog.state.descriptors[ref.Address], s.op.session)
		if utility, ok := stmt.(*parser.AlterView); ok && utility.Materialized {
			materializedAlterAuthority = auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
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
				typedSourceRef = e.domain.Ref(auth.catalog.Epoch(), source.address)
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
		if err := s.acquireSequence(typedSourceRef, accessShare, false); err != nil {
			return err
		}
	}
	if _, reading := stmt.(*parser.Select); !reading {
		if err = s.acquirePartitionAncestors(ref); err != nil {
			return err
		}
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
		s.admissionWaited = true
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		err = request.wait()
	}
	if err != nil {
		// Parse analysis opens SELECT/INSERT/UPDATE/DELETE with a positioned
		// error callback. Cached execution, COPY targets and utility locks do not.
		// Query cancellation (57014) deliberately bypasses that callback.
		_, copyTarget := stmt.(*parser.Copy)
		if !copyTarget && (mode == accessShare || mode == relationRowShare || mode == rowExclusive) && (!execute || s.Expected == nil) {
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
			d, exists := e.privateCatalogLocked(s.owner.catalog).state.descriptors[ref.Address].(*relationDescriptor)
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
			if err = s.acquirePartitionDescendants(ref, accessExclusive); err != nil {
				return err
			}
		}
	case *parser.CreateIndex:
		if err = s.acquirePartitionDescendants(ref, relationShare); err != nil {
			return err
		}
	case *parser.Select, *parser.Insert:
		if err = s.acquirePartitionDescendants(ref, accessShare); err != nil {
			return err
		}
	case *parser.Copy:
		if creation.From {
			if err = s.acquirePartitionDescendants(ref, accessShare); err != nil {
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
	descriptor, live := e.privateCatalogLocked(s.owner.catalog).state.descriptors[ref.Address].(*relationDescriptor)
	if ref.Domain() != e.domain || ref.Epoch != e.state.epoch || txref.Epoch() != e.state.epoch || !live {
		return ErrCatalogReplaced
	}
	if materializedAlterAuthority != nil &&
		(descriptor.relationKind != materializedAlterAuthority.relationKind ||
			!relationDefiningAuthorityCompatible(materializedAlterAuthority, descriptor) ||
			descriptor.sqlIndex != materializedAlterAuthority.sqlIndex) {
		// Check current ownership on the admitted identity, not its old name.
		// A still-authorized actor cannot adopt a changed defining predecessor.
		currentAuth, authorityErr := e.statementAuthorizationLocked(s.op, authorityStmt)
		if authorityErr != nil {
			return authorityErr
		}
		if authorityErr = alterViewAuthority(currentAuth, ref, stmt.(*parser.AlterView)); authorityErr != nil {
			return authorityErr
		}
		return ErrCatalogReplaced
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
			storageTarget = descriptor.relationKind == tableRelation || descriptor.relationKind == materializedViewRelation
		case *parser.Delete, *parser.Update, *parser.Merge:
			storageTarget = descriptor.relationKind == tableRelation
		case *parser.Insert:
			storageTarget = descriptor.relationKind == tableRelation && (len(s.mutations) != 0 || statement.Conflict != nil && statement.Conflict.Update)
		}
		if storageTarget {
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
		ns := s.viewCreationNamespace
		if s.viewChanges != nil {
			auth.catalog = e.privateCatalogLocked(s.viewChanges)
			auth.recaptureCatalog(auth.catalog)
		}
		if auth.catalog.state.names[ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns.Address, Name: name.Parts[len(name.Parts)-1].Text}] != ref.Address {
			return ErrCatalogReplaced
		}
		if err := auth.CheckCreate(ns); err != nil {
			return err
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
			return auth.CheckCreate(e.domain.Ref(ref.Epoch, descriptor.namespace))
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
	catalog := e.privateCatalogLocked(s.owner.catalog)
	for _, source := range s.inheritanceRelations {
		if _, err := currentQuerySourceLocked(e, catalog, s.owner.catalog, source); err != nil {
			return err
		}
	}
	for i, source := range s.querySources {
		current, err := currentQuerySourceLocked(e, catalog, s.owner.catalog, source)
		if err != nil {
			return err
		}
		if refreshSources && (current.relationKind != materializedViewRelation || s.view == nil) {
			s.querySources[i].relation = current
			if current.relationKind == materializedViewRelation {
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
	catalog := e.privateCatalogLocked(s.owner.catalog)
	if s.lockedRef.Domain() != e.domain || s.lockedRef.Epoch != e.state.epoch {
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
	for _, target := range s.inheritedTargets {
		if _, err := currentQuerySourceLocked(e, catalog, s.owner.catalog, target); err != nil {
			return err
		}
	}
	for address, captured := range s.partitionRelations {
		current, _ := catalog.state.descriptors[address].(*relationDescriptor)
		if current != captured && !partitionAppendCompatible(captured, current) {
			return ErrCatalogReplaced
		}
	}
	if s.viewWrite != nil {
		for _, layer := range s.viewWrite.layers {
			current, ok := catalog.state.descriptors[layer.ref.Address].(*relationDescriptor)
			if !ok || current.view != layer.relation.view || !sameRelationRowShape(current, layer.relation) {
				return ErrCatalogReplaced
			}
		}
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
