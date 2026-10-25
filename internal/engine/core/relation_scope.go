package core

import (
	"errors"
	"fmt"
	"strconv"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Resolve original authority before waiting; never resolve a replacement by name.
func (s *StatementScope) AcquireRelation(stmt parser.Statement, execute bool) (err error) {
	if foreign, ok := stmt.(*parser.ForeignDDL); ok {
		if foreign.Kind == "table" && foreign.Action == "alter" {
			stmt = fdw.AlterAdmission(foreign)
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
	if parser.HasModifyingCTE(dml.StatementCTERoot(stmt)) {
		return s.acquireCTEMutations(stmt, execute)
	}
	if merge, ok := stmt.(*parser.Merge); ok {
		if err := analyze.ValidateMergeReachability(merge); err != nil {
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
			_, err = s.AcquireViewSources(false)
		}
		if err == nil && s.admissionWaited {
			e := s.Engine()
			e.Mu.Lock()
			auth, authErr := s.statementAuthorizationLocked(stmt)
			err = authErr
			if err == nil {
				err = s.ValidateAdmissionNamesLocked(e, auth)
			}
			e.Mu.Unlock()
		}
	}()
	if creation, ok := stmt.(*parser.CreateTable); ok && execute {
		if creation.PartitionOf != nil {
			// Select the active utility pair before relation waits, outside
			// Engine.mu so SSI can wait safely, and only after the existing
			// execution restrictions. DEFAULT integrity validation separately
			// uses its grant-time frontier.
			if err := s.readOnlyUtilityEligibility(creation); err != nil {
				return err
			}
			if err := s.executionEligibility(creation); err != nil {
				return err
			}
			if err := s.snapshotDataPhase(); err != nil {
				return err
			}
		}
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
		if err := s.AcquireQueryRelations(view.Query, stmt, execute); err != nil {
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
		return s.AcquireQueryRelations(creation.Query, stmt, execute)
	}
	if declaration, ok := stmt.(*parser.DeclareCursor); ok {
		return s.AcquireQueryRelations(declaration.Query, declaration.Query, execute)
	}
	if sel, ok := stmt.(*parser.Select); ok {
		return s.AcquireQueryRelations(sel, stmt, execute)
	}
	if copy, ok := stmt.(*parser.Copy); ok && copy.Query != nil {
		return s.AcquireQueryRelations(copy.Query, copy.Query, execute)
	}
	if insert, ok := stmt.(*parser.Insert); ok {
		if err := s.acquireRelationTarget(stmt, stmt, execute, false); err != nil {
			return err
		}
		// PostgreSQL validates explicit target identifiers before opening the
		// source, so a blocked/missing source cannot mask a bad target list.
		if err := analyze.BindInsertTargets(s.LockedDescriptor.(*catalog.Relation), insert.Targets, nil); err != nil {
			return err
		}
		return s.AcquireQueryRelations(stmt, stmt, execute)
	}
	if _, ok := stmt.(*parser.CreateRule); ok {
		if !execute {
			return nil
		}
		// Only the event precedes qualification analysis. executeRule admits
		// each action after the preceding canonical binding completes.
		return s.acquireRelationTarget(stmt, stmt, true, false)
	}
	switch stmt.(type) {
	case *parser.CreatePolicy, *parser.AlterPolicy:
		if !execute {
			return nil
		}
		if err := s.acquireRelationTarget(stmt, stmt, execute, false); err != nil {
			return err
		}
		return s.AcquireQueryRelations(stmt, stmt, execute)
	case *parser.Update, *parser.Delete, *parser.Merge:
		if err := s.acquireRelationTarget(stmt, stmt, execute, false); err != nil {
			return err
		}
		if err := s.acquireInheritedMutationTargets(stmt); err != nil {
			return err
		}
		return s.AcquireQueryRelations(stmt, stmt, execute)
	}
	return s.acquireRelationTarget(stmt, stmt, execute, false)
}

// relationTargetEntries decide the relation a statement targets; each
// statement kind belongs to one package's entry.
var relationTargetEntries = []table.TargetEntry{view.TargetAdmission, table.TargetAdmission, dml.TargetAdmission}

// AcquireRelationTarget admits the relation stmt targets for family code.
// The body stays acquireRelationTarget: read-only reference fixtures wait on
// that frame.
func (s *StatementScope) AcquireRelationTarget(stmt, authorityStmt parser.Statement, execute, source bool) error {
	return s.acquireRelationTarget(stmt, authorityStmt, execute, source)
}

func (s *StatementScope) acquireRelationTarget(stmt, authorityStmt parser.Statement, execute, source bool) error {
	target, ok, err := table.AdmitTarget(relationTargetEntries, stmt, execute)
	if err != nil || !ok {
		return err
	}
	name, mode, ownerOnly := target.Name, target.Mode, target.OwnerOnly
	rename, ifExists, tableRename := target.Rename, target.IfExists, target.TableRename
	viewReplacement := target.ViewReplacement
	policy := lock.Policy{TimeoutSlot: &s.Op.LockWait, Timeout: s.Op.LockTimeout, StatementDeadline: s.Op.StatementDeadline, Source: s.Op.SourceContext}
	e := s.Engine()
	e.Mu.Lock()
	if s.TxRef.Epoch() != e.Catalog.State.Epoch() {
		e.Mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	auth, err := s.statementAuthorizationLocked(authorityStmt)
	parentAuth := auth
	if err == nil {
		err = view.PolicyPreflight(s.Op, auth.View, stmt, true)
	}
	if _, attaching := stmt.(*parser.AlterTableAttachPartition); attaching && err == nil {
		parentAuth, err = e.PartitionAttachAuthorization(s.Op, auth)
	}
	if creation, ok := stmt.(*parser.CreateTable); ok && err == nil {
		parentAuth, err = e.PartitionParentAuthorization(s.Op, auth, creation)
		if err == nil {
			err = s.checkPartitionCreationNamespaceLocked(auth, creation)
		}
	}
	var ref ids.ObjectRef
	retained := false
	if err == nil {
		if selection, ok := stmt.(*parser.Select); source && ok {
			if table, ok := selection.From.(*parser.TableReference); ok {
				ref = s.QueryLockAdmission.TableQueries[table].Ref
			}
		} else if s.prepared.reuse != nil {
			switch stmt.(type) {
			case *parser.Insert, *parser.Update, *parser.Delete, *parser.Merge:
				if s.prepared.reuse.targets != nil {
					ref = s.prepared.reuse.targets[stmt]
				} else {
					ref = s.prepared.reuse.target
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
			ns := s.ViewCreationOrZero().Namespace
			if s.ViewCreationOrZero().Changes != nil {
				auth.Catalog = e.PrivateCatalogLocked(s.ViewCreationOrZero().Changes)
				auth.Recapture(auth.Catalog)
			}
			err = auth.CheckCreate(ns)
			if err == nil {
				s.viewCreation().Namespace = ns
				address := auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns.Address, Name: name.Parts[len(name.Parts)-1].Text})
				if address != (ids.ObjectAddress{}) {
					ref = e.Catalog.Domain.Ref(auth.Catalog.Epoch(), address)
				}
			}
		} else if !rename && (mode == lock.AccessShare || mode == lock.RowShare || mode == lock.RowExclusive) {
			if _, copyTarget := stmt.(*parser.Copy); copyTarget {
				ref, err = e.ResolveRelationReference(auth, name, true)
			} else {
				ref, err = e.ResolveReference(auth, name)
			}
		} else {
			_, indexBuild := stmt.(*parser.CreateIndex)
			_, partitionCreate := stmt.(*parser.CreateTable)
			ref, err = e.ResolveRelationReference(parentAuth, name, mode == lock.ShareUpdateExclusive || rename || indexBuild || partitionCreate)
		}
	}
	if rename && ifExists && table.RenameMissing(err) {
		err = nil
	}
	if err == nil {
		switch utility := stmt.(type) {
		case *parser.CreateView:
			if ref.Domain() != nil {
				err = auth.Check(access.Requirement{Object: ref, OwnerOnly: true})
				if err == nil {
					d := auth.Catalog.MustRelation(ref.Address)
					if d.RelationKind != catalog.ViewRelation {
						err = SQLError("42809", fmt.Sprintf(`"%s" is not a view`, d.Name()))
					}
				}
			}
		case *parser.AlterView:
			if ref.Domain() != nil {
				err = view.AlterViewAuthority(parentAuth.View, parentAuth, ref, utility)
			}
		}
		if ownerOnly && !viewReplacement && err == nil {
			if ref.Domain() != nil {
				err = parentAuth.Check(access.Requirement{Object: ref, OwnerOnly: true, OwnerNoun: view.PolicyOwnerNoun(stmt)})
				if _, altering := stmt.(*parser.AlterTable); altering && err == nil {
					err = table.AlterTableLookupKind(parentAuth.Catalog.MustRelation(ref.Address))
				}
			}
			if tableRename && ref.Domain() != nil && err == nil {
				err = auth.CheckCreate(e.Catalog.Domain.Ref(ref.Epoch, auth.Catalog.Get(ref.Address).Namespace()))
			}
		}
		if err == nil && ref.Domain() != nil {
			err = partition.ConsumerBoundary(stmt, auth.Catalog.MustRelation(ref.Address), partitionReplayAllowed(s.Op, auth))
		}
	}
	var definitionStamp ids.DefinitionRevision
	var materializedAlterAuthority *catalog.Relation
	if err == nil && ref.Domain() != nil {
		definitionStamp = PreparedDefinitionStamp(auth.Catalog.Get(ref.Address), s.Op.Session)
		if utility, ok := stmt.(*parser.AlterView); ok && utility.Materialized {
			materializedAlterAuthority = auth.Catalog.MustRelation(ref.Address)
		}
		_, attaching := stmt.(*parser.AlterTableAttachPartition)
		_, detaching := stmt.(*parser.AlterTableDetachPartition)
		alter, altering := stmt.(*parser.AlterTable)
		if source || session.PreparableBody(stmt) || attaching || detaching || altering && table.HasRelationshipActions(alter.Actions) {
			lexical := ref
			if retained {
				lexical, err = e.AdmissionNameReference(auth, name)
			}
			if source {
				table := stmt.(*parser.Select).From.(*parser.TableReference)
				entry := s.QueryLockAdmission.TableQueries[table]
				entry.Lexical = lexical
				s.QueryLockAdmission.TableQueries[table] = entry
			} else {
				s.AdmittedTargetName, s.AdmittedTargetLexical = name, lexical
			}
		}
	}
	var typedSourceRef ids.ObjectRef
	if attachment, ok := stmt.(*parser.AlterTableOf); ok && attachment.Type != nil && err == nil {
		var source *catalog.Relation
		_, source, err = table.ResolveTypedSource(DDLHost{E: e, Op: s.Op}, auth, *attachment.Type)
		if err == nil {
			typedSourceRef = e.Catalog.Domain.Ref(auth.Catalog.Epoch(), source.Address())
		}
	}
	if raw, ok := stmt.(*parser.AlterTable); ok && err == nil {
		err = s.captureInheritanceRelationshipsLocked(e, auth, raw, ref)
	}
	e.Mu.Unlock()
	// Catalog provider validation may call Reset; ALTER must not retain a
	// replacement read admission across validation or publication.
	if ownerOnly && s.ReplacementRead {
		e.ReplacementMu.RUnlock()
		s.ReplacementRead = false
	}
	if err != nil {
		return err
	}
	if rename && ref.Domain() == nil {
		return nil
	}
	if typedSourceRef.Domain() != nil {
		if err := s.AcquireSequence(typedSourceRef, lock.AccessShare, false); err != nil {
			return err
		}
	}
	if _, reading := stmt.(*parser.Select); !reading {
		if err = s.acquirePartitionAncestors(ref); err != nil {
			return err
		}
	}
	request, err := e.Locks.StartAcquire(s.Op.Context(), s.LockTicket, lock.Resource{ObjectRef: ref}, mode, policy)
	if errors.Is(err, lock.ErrUnavailable) {
		relationName := name.Parts[0].Text
		if len(name.Parts) == 2 {
			relationName += "." + name.Parts[1].Text
		}
		return SQLError("55P03", fmt.Sprintf("could not obtain lock on relation \"%s\"", relationName))
	}
	if request != nil {
		s.admissionWaited = true
		if s.ReplacementRead {
			e.ReplacementMu.RUnlock()
			s.ReplacementRead = false
		}
		err = request.Wait()
	}
	if err != nil {
		// Parse analysis opens SELECT/INSERT/UPDATE/DELETE with a positioned
		// error callback. CREATE VIEW analyzes its body during utility execution,
		// even for a held handle; its quiet Describe has no cached source analysis.
		// Cached query execution, COPY targets and other utility locks do not.
		// Query cancellation (57014) deliberately bypasses that callback.
		_, copyTarget := stmt.(*parser.Copy)
		_, viewDefinition := authorityStmt.(*parser.CreateView)
		if !copyTarget && (mode == lock.AccessShare || mode == lock.RowShare || mode == lock.RowExclusive) && (!execute || s.Expected == nil || viewDefinition) {
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
	e.Mu.Lock()
	if ref.Domain() != nil {
		err = s.ObservePartitionRelationLocked(e, ref)
		if action, ok := stmt.(*parser.AlterTable); ok && err == nil {
			d, exists := e.PrivateCatalogLocked(s.Owner.Catalog).Relation(ref.Address)
			partitionDrop = exists && catalog.HierarchyRelation(d) && index.PartitionConstraintDropActions(d, action.Actions)
		}
	}
	e.Mu.Unlock()
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
			if err = s.AcquirePartitionDescendants(ref, lock.AccessExclusive); err != nil {
				return err
			}
		}
	case *parser.CreateIndex:
		if err = s.AcquirePartitionDescendants(ref, lock.Share); err != nil {
			return err
		}
	case *parser.CreateTrigger:
		if err = s.AcquirePartitionDescendants(ref, lock.ShareRowExclusive); err != nil {
			return err
		}
	case *parser.Select, *parser.Insert:
		if err = s.AcquirePartitionDescendants(ref, lock.AccessShare); err != nil {
			return err
		}
	case *parser.Update:
		if !creation.Only {
			if err = s.AcquirePartitionDescendants(ref, lock.RowExclusive); err != nil {
				return err
			}
		}
	case *parser.Copy:
		if creation.From {
			if err = s.AcquirePartitionDescendants(ref, lock.AccessShare); err != nil {
				return err
			}
		}
	}
	s.Owner.Mu.Lock()
	implicit := s.Owner.State == session.TxImplicit
	txref := s.Owner.Ref
	s.Owner.Mu.Unlock()
	if implicit && !ownerOnly && !s.ReplacementRead {
		e.ReplacementMu.RLock()
		s.ReplacementRead = true
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	descriptor, live := e.PrivateCatalogLocked(s.Owner.Catalog).Relation(ref.Address)
	if ref.Domain() != e.Catalog.Domain || ref.Epoch != e.Catalog.State.Epoch() || txref.Epoch() != e.Catalog.State.Epoch() || !live {
		return ids.ErrCatalogReplaced
	}
	if materializedAlterAuthority != nil &&
		(descriptor.RelationKind != materializedAlterAuthority.RelationKind ||
			!catalog.RelationDefiningAuthorityCompatible(materializedAlterAuthority, descriptor) ||
			descriptor.SQLIndex != materializedAlterAuthority.SQLIndex) {
		// Check current ownership on the admitted identity, not its old name.
		// A still-authorized actor cannot adopt a changed defining predecessor.
		currentAuth, authorityErr := s.statementAuthorizationLocked(authorityStmt)
		if authorityErr != nil {
			return authorityErr
		}
		if authorityErr = view.AlterViewAuthority(currentAuth.View, currentAuth, ref, stmt.(*parser.AlterView)); authorityErr != nil {
			return authorityErr
		}
		return ids.ErrCatalogReplaced
	}
	if PreparedDefinitionStamp(descriptor, s.Op.Session) != definitionStamp {
		s.prepared.changedDuringAdmission = true
	}
	if source {
		s.QuerySources = append(s.QuerySources, exec.Leaf{Ref: ref, Relation: descriptor, Only: table.SourceOnly(stmt), Storage: e.PrivateStorageLocked(s.Owner.Catalog, ref.Address)})
	} else {
		s.LockedRef = ref
		s.LockedDescriptor = descriptor
		storageTarget := false
		switch statement := stmt.(type) {
		case *parser.Copy:
			storageTarget = descriptor.RelationKind == catalog.TableRelation || descriptor.RelationKind == catalog.MaterializedViewRelation
		case *parser.Delete, *parser.Update, *parser.Merge:
			storageTarget = descriptor.RelationKind == catalog.TableRelation
		case *parser.Insert:
			storageTarget = descriptor.RelationKind == catalog.TableRelation && (len(s.mutations) != 0 || statement.Conflict != nil && statement.Conflict.Update)
		}
		if storageTarget {
			s.LockedStorage = e.PrivateStorageLocked(s.Owner.Catalog, ref.Address)
			if s.LockedStorage == nil {
				return errors.New("stuntgres: relation storage is missing")
			}
		}
		s.lockedIdentityOnly = mode == lock.ShareUpdateExclusive || mode == lock.Share
	}
	// Definition evolution while waiting is valid for this exact relation.
	// Pin the grant-time descriptor and revalidate current role authority.
	auth, err = s.statementAuthorizationLocked(authorityStmt)
	if err != nil {
		return err
	}
	if viewReplacement {
		ns := s.ViewCreationOrZero().Namespace
		if s.ViewCreationOrZero().Changes != nil {
			auth.Catalog = e.PrivateCatalogLocked(s.ViewCreationOrZero().Changes)
			auth.Recapture(auth.Catalog)
		}
		if auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns.Address, Name: name.Parts[len(name.Parts)-1].Text}) != ref.Address {
			return ids.ErrCatalogReplaced
		}
		if err := auth.CheckCreate(ns); err != nil {
			return err
		}
		if err := auth.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
			return err
		}
		if descriptor.RelationKind != catalog.ViewRelation {
			return SQLError("42809", fmt.Sprintf(`"%s" is not a view`, descriptor.Name()))
		}
		return nil
	}
	if ownerOnly {
		parentAuth = auth
		if creation, ok := stmt.(*parser.CreateTable); ok {
			parentAuth, err = e.PartitionParentAuthorization(s.Op, auth, creation)
			if err != nil {
				return err
			}
			if err := s.checkPartitionCreationNamespaceLocked(auth, creation); err != nil {
				return err
			}
		}
		if _, attaching := stmt.(*parser.AlterTableAttachPartition); attaching {
			parentAuth, err = e.PartitionAttachAuthorization(s.Op, auth)
			if err != nil {
				return err
			}
		}
		if err := parentAuth.Check(access.Requirement{Object: ref, OwnerOnly: true, OwnerNoun: view.PolicyOwnerNoun(stmt)}); err != nil {
			return err
		}
		if _, altering := stmt.(*parser.AlterTable); altering {
			if err := table.AlterTableLookupKind(descriptor); err != nil {
				return err
			}
		}
		if tableRename {
			return auth.CheckCreate(e.Catalog.Domain.Ref(ref.Epoch, descriptor.Namespace()))
		}
		return nil
	}
	return nil
}

func (s *StatementScope) ValidateLockedRelation(e *Engine) error {
	return s.validateRelationGrantsLocked(e, false)
}

// Final analysis refreshes compatible source metadata in the same critical
// section as authorization/binding. Later validation never rewrites layouts.
func (s *StatementScope) RefreshRelationGrantsLocked(e *Engine) error {
	return s.validateRelationGrantsLocked(e, true)
}

func (s *StatementScope) validateRelationGrantsLocked(e *Engine, refreshSources bool) error {
	if err := s.validateInheritanceRelationshipsLocked(e); err != nil {
		return err
	}
	if len(s.mutations) != 0 {
		active := s.ActiveMutation
		defer s.activateMutation(active)
		for i := range s.mutations {
			s.activateMutation(i)
			if err := s.validateActiveMutationLocked(e); err != nil {
				return err
			}
		}
		s.activateMutation(active)
	}
	if len(s.QuerySources) == 0 && s.LockedRef.Domain() == nil && len(s.PartitionRelations) == 0 {
		return nil
	}
	view := e.PrivateCatalogLocked(s.Owner.Catalog)
	for _, source := range s.Inheritance.relations {
		if _, err := CurrentQuerySourceLocked(e, view, s.Owner.Catalog, source); err != nil {
			return err
		}
	}
	for i, source := range s.QuerySources {
		current, err := CurrentQuerySourceLocked(e, view, s.Owner.Catalog, source)
		if err != nil {
			return err
		}
		if refreshSources && (current.RelationKind != catalog.MaterializedViewRelation || s.View == nil) {
			s.QuerySources[i].Relation = current
			if current.RelationKind == catalog.MaterializedViewRelation {
				s.QuerySources[i].Storage = e.PrivateStorageLocked(s.Owner.Catalog, source.Ref.Address)
			}
		}
	}
	if len(s.mutations) != 0 {
		return nil
	}
	return s.validateActiveMutationLocked(e)
}

func (s *StatementScope) validateActiveMutationLocked(e *Engine) error {
	if s.LockedRef.Domain() == nil {
		return nil
	}
	view := e.PrivateCatalogLocked(s.Owner.Catalog)
	if s.LockedRef.Domain() != e.Catalog.Domain || s.LockedRef.Epoch != e.Catalog.State.Epoch() {
		return ids.ErrCatalogReplaced
	}
	current := view.Get(s.LockedRef.Address)
	if s.lockedIdentityOnly {
		if _, ok := current.(*catalog.Relation); !ok {
			return ids.ErrCatalogReplaced
		}
	} else if s.LockedStorage != nil {
		relation, ok := current.(*catalog.Relation)
		captured, capturedOK := s.LockedDescriptor.(*catalog.Relation)
		if !ok || !capturedOK || s.LockedRef.Address.SubID != 0 || e.PrivateStorageLocked(s.Owner.Catalog, s.LockedRef.Address) != s.LockedStorage || !index.SameRelationRowShape(captured, relation) {
			return ids.ErrCatalogReplaced
		}
	} else if current != s.LockedDescriptor {
		return ids.ErrCatalogReplaced
	}
	for _, target := range s.InheritedTargets {
		if _, err := CurrentQuerySourceLocked(e, view, s.Owner.Catalog, target); err != nil {
			return err
		}
	}
	for address, captured := range s.PartitionRelations {
		current, _ := view.Relation(address)
		if current != captured && !partition.AppendCompatible(captured, current) {
			return ids.ErrCatalogReplaced
		}
	}
	if s.ViewWrite != nil {
		for _, layer := range s.ViewWrite.Layers {
			current, ok := view.Relation(layer.Ref.Address)
			if !ok || current.View != layer.Relation.View || !index.SameRelationRowShape(current, layer.Relation) {
				return ids.ErrCatalogReplaced
			}
		}
	}
	return nil
}

// Called under Engine.mu after partitionParentAuthorization validates any
// trusted importer provenance. Destination rights belong to the named child
// principal, never the parent's private restore authorization.
func (s *StatementScope) checkPartitionCreationNamespaceLocked(auth AuthorizationView, creation *parser.CreateTable) error {
	if creation.Persistence == "temporary" {
		return nil
	}
	ns, deferredTemp, err := s.Engine().creationNamespaceDestination(auth, creation.Name, false)
	if err == nil && !deferredTemp && !OperationReplayOrZero(s.Op).TableDestination {
		err = auth.CheckCreate(ns)
	}
	if err != nil {
		return ddl.CreationError(err, creation.Name)
	}
	return nil
}
