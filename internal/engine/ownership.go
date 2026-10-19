package engine

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Root classification follows canonical reciprocal identities, never object
// names. Protected catalog namespaces and immutable providers are not bulk roots.
func ownershipRoot(view catalog.Read, d catalog.Descriptor) bool {
	switch t := d.(type) {
	case *catalog.ForeignObject:
		return t.Address().ClassID != ids.ForeignMappingClassID || t.MappedRole != (authorization.PrincipalRef{})
	case *catalog.Namespace:
		return t.Temporary == (ids.SessionToken{}) && t.Address().ObjectID != ids.PGCatalogNamespaceOID && t.Address().ObjectID != ids.PGToastNamespaceOID && t.Address().ObjectID != 13229
	case *catalog.Relation:
		return t.RelationKind != catalog.CatalogRelation && t.RelationKind != catalog.IndexRelation && t.RelationKind != catalog.CompositeRelation && (t.Sequence == nil || t.Sequence.Owned == (ids.ObjectAddress{}))
	case *catalog.Type:
		if !t.SQLType.IsUserDefined() || t.SQLType.IsArray() {
			return false
		}
		if t.OwningRelation == (ids.ObjectAddress{}) {
			return true
		}
		r, ok := view.Relation(t.OwningRelation)
		return ok && r.RelationKind == catalog.CompositeRelation
	case *catalog.Collation:
		return !t.Seeded() && t.Owner != (authorization.PrincipalRef{})
	case *catalog.Conversion:
		return !t.Seeded() && t.Owner != (authorization.PrincipalRef{})
	case *catalog.Operator:
		return !operatorSeeded(t)
	case *catalog.Procedure:
		return !t.Seeded() && t.Foreign == nil
	case *catalog.Database, *catalog.Extension, *catalog.TSDictionary:
		return true
	}
	return false
}

// Ownership changes retain immutable type, row, index and sequence generator
// roots. Only descriptor/ACL payloads change. All reciprocal members are staged
// before the ordinary publication validator sees any of them.
func stageOwnership(stage *CatalogStage, d catalog.Descriptor, target authorization.PrincipalRef) error {
	if next := stage.Descriptors[d.Address()]; next != nil {
		d = next
	}
	owner, _, _ := access.DescriptorACL(d)
	if owner == target {
		return nil
	}
	revision, err := stage.cascadeMetadataRevision(d.Revision())
	if err != nil {
		return err
	}
	switch old := d.(type) {
	case *catalog.Database:
		next := *old
		next.Owner, next.ObjectRevision = target, revision
		next.ACL = authorization.SchemaACLOwner(old.ACL, old.Owner, target)
		return stage.Replace(&next)
	case *catalog.ForeignObject:
		next := *old
		next.Owner, next.ObjectRevision = target, revision
		next.ACL = authorization.SchemaACLOwner(old.ACL, old.Owner, target)
		return stage.Replace(&next)
	case *catalog.Extension:
		next := *old
		next.Owner, next.ObjectRevision = target, revision
		return stage.Replace(&next)
	case *catalog.Procedure:
		next := *old
		next.Owner, next.ObjectRevision = target, revision
		next.ACL = authorization.SchemaACLOwner(old.ACL, old.Owner, target)
		return stage.Replace(&next)
	case *catalog.TSDictionary:
		next := *old
		next.Owner, next.ObjectRevision = target, revision
		return stage.Replace(&next)
	case *catalog.Namespace:
		next := *old
		next.Owner = target
		next.ObjectRevision = revision
		next.ACL = authorization.SchemaACLOwner(old.ACL, old.Owner, target)
		return stage.Replace(&next)
	case *catalog.Collation:
		next := *old
		next.Owner = target
		next.ObjectRevision = revision
		return stage.Replace(&next)
	case *catalog.Conversion:
		next := *old
		next.Owner, next.ObjectRevision = target, revision
		return stage.Replace(&next)
	case *catalog.Operator:
		next := *old
		next.Owner, next.ObjectRevision = target, revision
		return stage.Replace(&next)
	case *catalog.Type:
		next := *old
		next.Owner = target
		next.ObjectRevision = revision
		next.ACL = authorization.SchemaACLOwner(old.ACL, old.Owner, target)
		if err = stage.Replace(&next); err != nil {
			return err
		}
		if old.Array != 0 {
			array, ok := stage.Base.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(old.Array)})
			if !ok || !array.SQLType.IsArray() || array.SQLType.Element().ID() != old.SQLType.ID() {
				return catalog.Conflict{}
			}
			if err = stageOwnership(stage, array, target); err != nil {
				return err
			}
		}
		if old.OwningRelation != (ids.ObjectAddress{}) && !old.SQLType.IsArray() {
			r, ok := stage.Base.Relation(old.OwningRelation)
			if !ok || r.RowType != old.SQLType.ID() {
				return catalog.Conflict{}
			}
			return stageOwnership(stage, r, target)
		}
	case *catalog.Relation:
		next := *old
		next.Owner = target
		next.ObjectRevision = revision
		next.Security.ACL = authorization.SchemaACLOwner(old.Security.ACL, old.Owner, target)
		if old.Sequence != nil {
			next.SequencePlanStamp = revision
		}
		if len(old.Security.ColumnACL) != 0 {
			next.Security.ColumnACL = make(map[int32]authorization.ACLValue, len(old.Security.ColumnACL))
			for sub, acl := range old.Security.ColumnACL {
				next.Security.ColumnACL[sub] = authorization.SchemaACLOwner(acl, old.Owner, target)
			}
		}
		// Replace synchronizes the row type and its array against this owner.
		if err = stage.Replace(&next); err != nil {
			return err
		}
		if old.RelationKind == catalog.TableRelation || old.RelationKind == catalog.PartitionedRelation || old.RelationKind == catalog.MaterializedViewRelation {
			for _, associated := range stage.Base.Descriptors() {
				r, ok := associated.(*catalog.Relation)
				if !ok {
					continue
				}
				linked := r.SQLIndex != nil && r.SQLIndex.Parent == old.Address()
				if r.Sequence != nil {
					parent := r.Sequence.Owned
					parent.SubID = 0
					linked = linked || parent == old.Address()
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
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, op.session)
	return stage
}
func (e *Engine) publishOwnership(scope *StatementScope, stage *CatalogStage) (Result, error) {
	defer e.DiscardCatalogStage(stage)
	if len(stage.Descriptors) == 0 && len(stage.Removals) == 0 && len(stage.DefaultACLs) == 0 && stage.roleCandidate == nil {
		if err := e.validateOperation(scope.op); err != nil {
			return Result{}, err
		}
		scope.completed = true
		return Result{}, scope.op.Context().Err()
	}
	prepared, err := e.ValidateCatalogStage(scope.op.Context(), stage)
	if err == nil {
		_, err = e.publishCatalogOperation(scope.op, scope, prepared, PublicationToken{Epoch: stage.Base.Epoch()})
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
		return fail(ids.ErrCatalogReplaced)
	}
	old := auth.Catalog.MustRelation(ref.Address)
	if kind == "materialized view" {
		if err := alterViewAuthority(auth, ref, &parser.AlterView{Materialized: true, Action: "owner"}); err != nil {
			return fail(err)
		}
	} else if kind == "view" && old.RelationKind != catalog.ViewRelation {
		return fail(sqlError("42809", fmt.Sprintf(`"%s" is not a %s`, old.Name(), kind)))
	}
	if old.RelationKind == catalog.CatalogRelation || old.RelationKind == catalog.IndexRelation || old.RelationKind == catalog.CompositeRelation {
		return fail(tableKind(old, "change"))
	}
	if kind != "materialized view" {
		if err = auth.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
			return fail(err)
		}
	}
	target, err := access.SchemaOwner(auth.View, targetName, identity)
	if err != nil {
		return fail(err)
	}
	if target == old.Owner {
		e.mu.Unlock()
		scope.completed = true
		return Result{}, scope.op.Context().Err()
	}
	if old.Sequence != nil && old.Sequence.Owned != (ids.ObjectAddress{}) {
		parent := old.Sequence.Owned
		parent.SubID = 0
		return fail(sequenceLinkedError(old, auth.Catalog.MustRelation(parent), false))
	}
	if err = access.OwnershipEligibility(auth.View, auth, target, old.Namespace()); err != nil {
		return fail(err)
	}
	stage := e.ownershipStage(auth, scope.op)
	err = stageOwnership(stage, old, target)
	e.mu.Unlock()
	if err == nil {
		for _, a := range ownershipRelationLocks(stage.Base, []ids.ObjectAddress{old.Address()}) {
			if a == old.Address() {
				continue
			}
			if err = scope.acquireSequence(e.catalog.Domain.Ref(stage.Base.Epoch(), a), lock.AccessExclusive, true); err != nil {
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
	name, identity := access.OwnershipRoleOperand(s.Target)
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
	var old *catalog.Type
	for i := range path.count() {
		a := auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: path.at(i), Name: s.Name.Parts[len(s.Name.Parts)-1].Text})
		if a != (ids.ObjectAddress{}) {
			old = auth.Catalog.MustType(a)
			break
		}
	}
	if old == nil {
		return fail(sqlError("42704", fmt.Sprintf(`type "%s" does not exist`, objectNameText(s.Name))))
	}
	diagnosticPath, _ := e.referencePath(auth)
	display := catalog.TypeDiagnosticNameAt(auth.Catalog, old, diagnosticPath)
	if s.Kind == "domain" && !old.SQLType.IsDomain() {
		return fail(sqlError("42809", display+" is not a domain"))
	}
	if old.SQLType.IsArray() {
		element := auth.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(old.SQLType.Element().ID())})
		elementName := catalog.TypeDiagnosticNameAt(auth.Catalog, element, diagnosticPath)
		return fail(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: "cannot alter array type " + display, Hint: "You can alter type " + elementName + ", which will alter the array type as well."})
	}
	if old.OwningRelation != (ids.ObjectAddress{}) {
		r := auth.Catalog.MustRelation(old.OwningRelation)
		if r.RelationKind != catalog.CompositeRelation {
			return fail(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: display + " is a table's row type", Hint: "Use ALTER TABLE instead."})
		}
	}
	target, err := access.SchemaOwner(auth.View, name, identity)
	if err != nil {
		return fail(err)
	}
	if !old.SQLType.IsUserDefined() {
		return fail(sqlError("0A000", "ownership of builtin types is unsupported"))
	}
	if target == old.Owner {
		e.mu.Unlock()
		scope.completed = true
		return Result{}, scope.op.Context().Err()
	}
	ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), old.Address())
	if err = auth.Check(access.Requirement{Object: ref, OwnerOnly: true, OwnerNoun: "type"}); err != nil {
		return fail(err)
	}
	if err = access.OwnershipEligibility(auth.View, auth, target, old.Namespace()); err != nil {
		return fail(err)
	}
	stage := e.ownershipStage(auth, scope.op)
	err = stageOwnership(stage, old, target)
	e.mu.Unlock()
	if err == nil {
		for _, a := range ownershipRelationLocks(stage.Base, []ids.ObjectAddress{old.Address()}) {
			if err = scope.acquireSequence(e.catalog.Domain.Ref(stage.Base.Epoch(), a), lock.AccessExclusive, true); err != nil {
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
	target, err := access.SchemaOwner(auth.View, name, identity)
	if err != nil {
		return fail(err)
	}
	database := auth.Catalog.MustDatabase(catalog.CurrentDatabaseAddress)
	if len(s.Name.Parts) != 1 || s.Name.Parts[0].Text != database.Name() {
		return fail(sqlError("3D000", fmt.Sprintf(`database "%s" does not exist`, s.Name.Parts[len(s.Name.Parts)-1].Text)))
	}
	if target == database.Owner {
		e.mu.Unlock()
		scope.completed = true
		return Result{}, scope.op.Context().Err()
	}
	if err = auth.Check(access.Requirement{Object: e.catalog.Domain.Ref(auth.Catalog.Epoch(), database.Address()), OwnerOnly: true}); err != nil {
		return fail(err)
	}
	if err = access.OwnershipSuccessor(auth.View, target); err != nil {
		return fail(err)
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
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
