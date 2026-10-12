package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func ownershipSuccessor(auth AuthorizationView, target PrincipalRef) error {
	allowed, err := auth.roles.canSetRole(auth.state.CurrentUser, target)
	if err != nil {
		return err
	}
	if !allowed {
		role, _ := auth.roles.Descriptor(target)
		return sqlError("42501", fmt.Sprintf(`must be able to SET ROLE "%s"`, role.Name))
	}
	return nil
}
func ownershipDatabaseCreate(auth AuthorizationView) error {
	return auth.CheckCreate(ObjectRef{owner: auth.engine.owner, Epoch: auth.catalog.Epoch(), Address: currentDatabaseAddress})
}
func ownershipEligibility(auth AuthorizationView, target PrincipalRef, namespace ObjectAddress) error {
	actor, err := auth.roles.Descriptor(auth.state.CurrentUser)
	if err != nil {
		return err
	}
	if actor.Superuser {
		return nil
	}
	if err = ownershipSuccessor(auth, target); err != nil {
		return err
	}
	targetAuth := auth
	targetAuth.state.CurrentUser = target
	if namespace == (ObjectAddress{}) {
		return nil
	}
	return targetAuth.CheckCreate(ObjectRef{owner: auth.engine.owner, Epoch: auth.catalog.Epoch(), Address: namespace})
}
func ownershipRoleOperand(spec parser.RoleSpec) (string, bool) {
	switch spec.Identity {
	case "current":
		return "current_user", true
	case "session":
		return "session_user", true
	}
	return spec.Name, false
}

// Root classification follows canonical reciprocal identities, never object
// names. Protected catalog namespaces and immutable providers are not bulk roots.
func ownershipRoot(catalog CatalogRead, d Descriptor) bool {
	switch t := d.(type) {
	case *namespaceDescriptor:
		return t.address.ObjectID != PGCatalogNamespaceOID && t.address.ObjectID != 13229
	case *relationDescriptor:
		return t.relationKind != catalogRelation && t.relationKind != indexRelation && t.relationKind != compositeRelation && (t.sequence == nil || t.sequence.owned == (ObjectAddress{}))
	case *typeDescriptor:
		if !t.typ.IsUserDefined() || t.typ.IsArray() {
			return false
		}
		if t.owningRelation == (ObjectAddress{}) {
			return true
		}
		r, ok := catalog.state.descriptors[t.owningRelation].(*relationDescriptor)
		return ok && r.relationKind == compositeRelation
	case *collationDescriptor:
		return !t.seeded() && t.owner != (PrincipalRef{})
	case *databaseDescriptor, *extensionDescriptor, *procedureDescriptor, *tsDictionaryDescriptor:
		return true
	}
	return false
}

// Ownership changes retain immutable type, row, index and sequence generator
// roots. Only descriptor/ACL payloads change. All reciprocal members are staged
// before the ordinary publication validator sees any of them.
func stageOwnership(stage *CatalogStage, d Descriptor, target PrincipalRef) error {
	if next := stage.descriptors[d.Address()]; next != nil {
		d = next
	}
	owner, _, _ := descriptorACL(d)
	if owner == target {
		return nil
	}
	revision, err := stage.cascadeMetadataRevision(d.Revision())
	if err != nil {
		return err
	}
	switch old := d.(type) {
	case *databaseDescriptor:
		next := *old
		next.owner, next.revision = target, revision
		next.acl = schemaACLOwner(old.acl, old.owner, target)
		return stage.Replace(&next)
	case *extensionDescriptor:
		next := *old
		next.owner, next.revision = target, revision
		return stage.Replace(&next)
	case *procedureDescriptor:
		next := *old
		next.owner, next.revision = target, revision
		return stage.Replace(&next)
	case *tsDictionaryDescriptor:
		next := *old
		next.owner, next.revision = target, revision
		return stage.Replace(&next)
	case *namespaceDescriptor:
		next := *old
		next.owner = target
		next.revision = revision
		next.acl = schemaACLOwner(old.acl, old.owner, target)
		return stage.Replace(&next)
	case *collationDescriptor:
		next := *old
		next.owner = target
		next.revision = revision
		return stage.Replace(&next)
	case *typeDescriptor:
		next := *old
		next.owner = target
		next.revision = revision
		next.acl = schemaACLOwner(old.acl, old.owner, target)
		if err = stage.Replace(&next); err != nil {
			return err
		}
		if old.array != 0 {
			array, ok := stage.base.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(old.array)}].(*typeDescriptor)
			if !ok || !array.typ.IsArray() || array.typ.Element().ID() != old.typ.ID() {
				return CatalogConflict{}
			}
			if err = stageOwnership(stage, array, target); err != nil {
				return err
			}
		}
		if old.owningRelation != (ObjectAddress{}) && !old.typ.IsArray() {
			r, ok := stage.base.state.descriptors[old.owningRelation].(*relationDescriptor)
			if !ok || r.rowType != old.typ.ID() {
				return CatalogConflict{}
			}
			return stageOwnership(stage, r, target)
		}
	case *relationDescriptor:
		next := *old
		next.owner = target
		next.revision = revision
		next.acl = schemaACLOwner(old.acl, old.owner, target)
		if old.sequence != nil {
			next.sequencePlanStamp = revision
		}
		if len(old.columnACL) != 0 {
			next.columnACL = make(map[int32]ACLValue, len(old.columnACL))
			for sub, acl := range old.columnACL {
				next.columnACL[sub] = schemaACLOwner(acl, old.owner, target)
			}
		}
		// Replace synchronizes the row type and its array against this owner.
		if err = stage.Replace(&next); err != nil {
			return err
		}
		if old.relationKind == tableRelation || old.relationKind == partitionedRelation || old.relationKind == materializedViewRelation {
			for _, associated := range stage.base.state.descriptors {
				r, ok := associated.(*relationDescriptor)
				if !ok {
					continue
				}
				linked := r.sqlIndex != nil && r.sqlIndex.parent == old.address
				if r.sequence != nil {
					parent := r.sequence.owned
					parent.SubID = 0
					linked = linked || parent == old.address
				}
				if linked {
					if err = stageOwnership(stage, r, target); err != nil {
						return err
					}
				}
			}
		}
	default:
		return fmt.Errorf("stuntgres: object has no ownership handler")
	}
	return nil
}
func (e *Engine) ownershipStage(auth AuthorizationView, op *Operation) *CatalogStage {
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authorizeLocked(auth, op.session)
	return stage
}
func (e *Engine) publishOwnership(scope *StatementScope, stage *CatalogStage) (Result, error) {
	defer e.DiscardCatalogStage(stage)
	if len(stage.descriptors) == 0 && len(stage.removals) == 0 && len(stage.defaultACLs) == 0 && stage.roleCandidate == nil {
		if err := e.validateOperation(scope.op); err != nil {
			return Result{}, err
		}
		scope.completed = true
		return Result{}, scope.op.Context().Err()
	}
	prepared, err := e.ValidateCatalogStage(scope.op.Context(), stage)
	if err == nil {
		_, err = e.publishCatalogOperation(scope.op, prepared, PublicationToken{Epoch: stage.base.Epoch()})
	}
	if err == nil {
		scope.completed = true
	}
	return Result{}, err
}
func (e *Engine) executeRelationOwnership(scope *StatementScope, name parser.ObjectName, targetName string, identity bool, kind string, ifExists bool, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.mu.Lock()
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	if err := scope.validateLockedRelation(e); err != nil {
		return fail(err)
	}
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		return fail(err)
	}
	ref, err := e.resolveRelationReference(auth, name, true)
	if ifExists && renameMissing(err) {
		e.mu.Unlock()
		scope.completed = true
		scope.op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`relation "%s" does not exist, skipping`, name.Parts[len(name.Parts)-1].Text)})
		return Result{}, scope.op.Context().Err()
	}
	if err != nil {
		return fail(err)
	}
	if ref != scope.lockedRef {
		return fail(ErrCatalogReplaced)
	}
	old := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
	if kind == "materialized view" {
		if err := alterViewAuthority(auth, ref, &parser.AlterView{Materialized: true, Action: "owner"}); err != nil {
			return fail(err)
		}
	} else if kind == "view" && old.relationKind != viewRelation {
		return fail(sqlError("42809", fmt.Sprintf(`"%s" is not a %s`, old.name, kind)))
	}
	if old.relationKind == catalogRelation || old.relationKind == indexRelation || old.relationKind == compositeRelation {
		return fail(tableKind(old, "change"))
	}
	if kind != "materialized view" {
		if err = auth.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
			return fail(err)
		}
	}
	target, err := schemaOwner(auth, targetName, identity)
	if err != nil {
		return fail(err)
	}
	if target == old.owner {
		e.mu.Unlock()
		scope.completed = true
		return Result{}, scope.op.Context().Err()
	}
	if old.sequence != nil && old.sequence.owned != (ObjectAddress{}) {
		parent := old.sequence.owned
		parent.SubID = 0
		return fail(sequenceLinkedError(old, auth.catalog.state.descriptors[parent].(*relationDescriptor), false))
	}
	if err = ownershipEligibility(auth, target, old.namespace); err != nil {
		return fail(err)
	}
	stage := e.ownershipStage(auth, scope.op)
	err = stageOwnership(stage, old, target)
	e.mu.Unlock()
	if err == nil {
		for _, a := range ownershipRelationLocks(stage.base, []ObjectAddress{old.address}) {
			if a == old.address {
				continue
			}
			if err = scope.acquireSequence(ObjectRef{owner: e.owner, Epoch: stage.base.Epoch(), Address: a}, accessExclusive, true); err != nil {
				break
			}
		}
	}
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	if err != nil {
		e.DiscardCatalogStage(stage)
		return Result{}, err
	}
	return e.publishOwnership(scope, stage)
}
func (e *Engine) executeOwnership(scope *StatementScope, s *parser.AlterOwner, count int) (Result, error) {
	name, identity := ownershipRoleOperand(s.Target)
	if s.Kind == "database" {
		return e.executeDatabaseOwnership(scope, s, name, identity, count)
	}
	if s.Kind != "type" && s.Kind != "domain" {
		return e.executeRelationOwnership(scope, s.Name, name, identity, s.Kind, s.IfExists, count)
	}
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.mu.Lock()
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		return fail(err)
	}
	path, err := e.lookupNamespaces(auth, s.Name, typeLookup)
	if err != nil {
		return fail(err)
	}
	var old *typeDescriptor
	for i := range path.count() {
		a := auth.catalog.state.names[NameKey{ClassID: TypeClassID, Namespace: path.at(i), Name: s.Name.Parts[len(s.Name.Parts)-1].Text}]
		if a != (ObjectAddress{}) {
			old = auth.catalog.state.descriptors[a].(*typeDescriptor)
			break
		}
	}
	if old == nil {
		return fail(sqlError("42704", fmt.Sprintf(`type "%s" does not exist`, objectNameText(s.Name))))
	}
	diagnosticPath, _ := e.referencePath(auth)
	display := typeDiagnosticNameAt(auth.catalog, old, diagnosticPath)
	if s.Kind == "domain" && !old.typ.IsDomain() {
		return fail(sqlError("42809", display+" is not a domain"))
	}
	if old.typ.IsArray() {
		element := auth.catalog.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(old.typ.Element().ID())}].(*typeDescriptor)
		elementName := typeDiagnosticNameAt(auth.catalog, element, diagnosticPath)
		return fail(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: "cannot alter array type " + display, Hint: "You can alter type " + elementName + ", which will alter the array type as well."})
	}
	if old.owningRelation != (ObjectAddress{}) {
		r := auth.catalog.state.descriptors[old.owningRelation].(*relationDescriptor)
		if r.relationKind != compositeRelation {
			return fail(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: display + " is a table's row type", Hint: "Use ALTER TABLE instead."})
		}
	}
	target, err := schemaOwner(auth, name, identity)
	if err != nil {
		return fail(err)
	}
	if !old.typ.IsUserDefined() {
		return fail(sqlError("0A000", "ownership of builtin types is unsupported"))
	}
	if target == old.owner {
		e.mu.Unlock()
		scope.completed = true
		return Result{}, scope.op.Context().Err()
	}
	ref := ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: old.Address()}
	if err = auth.Check(AccessRequirement{Object: ref, OwnerOnly: true, OwnerNoun: "type"}); err != nil {
		return fail(err)
	}
	if err = ownershipEligibility(auth, target, old.Namespace()); err != nil {
		return fail(err)
	}
	stage := e.ownershipStage(auth, scope.op)
	err = stageOwnership(stage, old, target)
	e.mu.Unlock()
	if err == nil {
		for _, a := range ownershipRelationLocks(stage.base, []ObjectAddress{old.Address()}) {
			if err = scope.acquireSequence(ObjectRef{owner: e.owner, Epoch: stage.base.Epoch(), Address: a}, accessExclusive, true); err != nil {
				break
			}
		}
	}
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	if err != nil {
		e.DiscardCatalogStage(stage)
		return Result{}, err
	}
	return e.publishOwnership(scope, stage)
}
func (e *Engine) executeDatabaseOwnership(scope *StatementScope, s *parser.AlterOwner, name string, identity bool, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.mu.Lock()
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		return fail(err)
	}
	target, err := schemaOwner(auth, name, identity)
	if err != nil {
		return fail(err)
	}
	database := auth.catalog.state.descriptors[currentDatabaseAddress].(*databaseDescriptor)
	if len(s.Name.Parts) != 1 || s.Name.Parts[0].Text != database.name {
		return fail(sqlError("3D000", fmt.Sprintf(`database "%s" does not exist`, s.Name.Parts[len(s.Name.Parts)-1].Text)))
	}
	if target == database.owner {
		e.mu.Unlock()
		scope.completed = true
		return Result{}, scope.op.Context().Err()
	}
	if err = auth.Check(AccessRequirement{Object: ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: database.address}, OwnerOnly: true}); err != nil {
		return fail(err)
	}
	if err = ownershipSuccessor(auth, target); err != nil {
		return fail(err)
	}
	actor, err := auth.roles.Descriptor(auth.state.CurrentUser)
	if err != nil {
		return fail(err)
	}
	if !actor.Superuser && !actor.CreateDB {
		return fail(sqlError("42501", "permission denied to change owner of database"))
	}
	stage := e.ownershipStage(auth, scope.op)
	err = stageOwnership(stage, database, target)
	e.mu.Unlock()
	if err != nil {
		e.DiscardCatalogStage(stage)
		return Result{}, err
	}
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	return e.publishOwnership(scope, stage)
}
