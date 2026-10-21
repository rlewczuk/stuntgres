package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (e *Engine) ownershipStage(auth AuthorizationView, op *session.Operation) *CatalogStage {
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, op.Session)
	return stage
}
func (e *Engine) publishOwnership(scope *StatementScope, stage *CatalogStage) (Result, error) {
	defer e.DiscardCatalogStage(stage)
	if stage.DescriptorCount() == 0 && stage.RemovalCount() == 0 && stage.DefaultACLCount() == 0 && stage.roleCandidate == nil {
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
		scope.op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`relation "%s" does not exist, skipping`, name.Parts[len(name.Parts)-1].Text)})
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
		if err := alterViewAuthority(auth.View, auth, ref, &parser.AlterView{Materialized: true, Action: "owner"}); err != nil {
			return fail(err)
		}
	} else if kind == "view" && old.RelationKind != catalog.ViewRelation {
		return fail(sqlError("42809", fmt.Sprintf(`"%s" is not a %s`, old.Name(), kind)))
	}
	if old.RelationKind == catalog.CatalogRelation || old.RelationKind == catalog.IndexRelation || old.RelationKind == catalog.CompositeRelation {
		return fail(analyze.TableKindError(old, "change"))
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
		return fail(sequence.LinkedError(old, auth.Catalog.MustRelation(parent), false))
	}
	if err = access.OwnershipEligibility(auth.View, auth, target, old.Namespace()); err != nil {
		return fail(err)
	}
	stage := e.ownershipStage(auth, scope.op)
	err = stageOwnership(stage, old, target)
	e.mu.Unlock()
	if err == nil {
		for _, a := range access.OwnershipRelationLocks(stage.Base, []ids.ObjectAddress{old.Address()}) {
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
	path, err := e.lookupNamespaces(auth, s.Name, analyze.TypeLookup)
	if err != nil {
		return fail(err)
	}
	var old *catalog.Type
	for i := range path.Count() {
		a := auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: path.At(i), Name: s.Name.Parts[len(s.Name.Parts)-1].Text})
		if a != (ids.ObjectAddress{}) {
			old = auth.Catalog.MustType(a)
			break
		}
	}
	if old == nil {
		return fail(sqlError("42704", fmt.Sprintf(`type "%s" does not exist`, catalog.ObjectNameText(s.Name))))
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
		for _, a := range access.OwnershipRelationLocks(stage.Base, []ids.ObjectAddress{old.Address()}) {
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
