package engine

import (
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// The owner-authorized AX grant pins the private parent incarnation. Rename is
// metadata-only: the existing overlay and Finish own publication of the group.
// Engine.mu is held on entry and released on every return.
func (e *Engine) renameConstraintLocked(scope *StatementScope, op *Operation, auth AuthorizationView, raw *parser.AlterTableRenameConstraint, count int) (Result, error) {
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	ref, err := e.resolveRelationReference(auth, raw.Name, true)
	if raw.IfExists && renameMissing(err) {
		return e.renameRelationLocked(scope, op, auth, raw.Name, "", raw.NewName.Text, true, false, false, false, count)
	}
	if err != nil {
		return fail(err)
	}
	if ref != scope.lockedRef {
		return fail(ErrCatalogReplaced)
	}
	if err := auth.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
		return fail(err)
	}
	parent := scope.lockedDescriptor.(*relationDescriptor)
	if parent.relationKind == sequenceRelation {
		return fail(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot rename columns of relation "%s"`, parent.name), Detail: "This operation is not supported for sequences."})
	}
	// PostgreSQL accepts view/index targets here, then performs constraint lookup.
	var backing ids.ObjectAddress
	kind := byte(0)
	for _, c := range parent.notNullConstraints {
		if c.Name == raw.Constraint.Text {
			kind = 'n'
			break
		}
	}
	for _, c := range parent.checkConstraints {
		if c.Name == raw.Constraint.Text {
			kind = 'c'
			break
		}
	}
	for _, c := range parent.uniqueConstraints {
		if c.Name == raw.Constraint.Text {
			kind = 'u'
			backing = c.Index
			break
		}
	}
	for _, c := range parent.exclusionConstraints {
		if c.Name == raw.Constraint.Text {
			kind = 'x'
			backing = c.Index
			break
		}
	}
	for _, c := range parent.foreignKeys {
		if c.Name == raw.Constraint.Text {
			kind = 'f'
			break
		}
	}
	if kind == 0 {
		return fail(sqlError("42704", fmt.Sprintf(`constraint "%s" for table "%s" does not exist`, raw.Constraint.Text, parent.name)))
	}
	if (kind == 'n' || kind == 'c') && inheritanceParticipant(auth.catalog, parent) {
		return e.renameInheritanceConstraintLocked(scope, op, auth, raw, parent, kind)
	}
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authorizeLocked(auth, op.session)
	if backing != (ids.ObjectAddress{}) {
		index, ok := auth.catalog.state.descriptors[backing].(*relationDescriptor)
		if !ok {
			e.DiscardCatalogStage(stage)
			return fail(ErrCatalogReplaced)
		}
		if _, exists := auth.catalog.state.names[ids.NameKey{ClassID: RelationClassID, Namespace: parent.namespace, Name: raw.NewName.Text}]; exists {
			e.DiscardCatalogStage(stage)
			return fail(sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, raw.NewName.Text)))
		}
		if index.revision == ids.DefinitionRevision(^uint64(0)) {
			e.DiscardCatalogStage(stage)
			return fail(fmt.Errorf("stuntgres: definition revision exhausted"))
		}
		next := *index
		next.name = raw.NewName.Text
		next.revision++
		err = stageBackingIndexRename(stage, &next)
		if err == nil {
			err = stage.Replace(&next)
		}
	} else {
		var next *relationDescriptor
		next, err = renameLocalConstraint(parent, raw.Constraint.Text, raw.NewName.Text, kind)
		if err == nil {
			err = stage.Replace(next)
		}
	}
	if err != nil {
		e.DiscardCatalogStage(stage)
		return fail(err)
	}
	changes := cloneCatalogChanges(scope.owner.catalog)
	grantCatalog := e.state
	scope.catalogAuthority = auth.catalogAuthorityLocked(ref, grantCatalog.descriptors[ref.Address])
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if _, err := e.ValidateCatalogStage(op.Context(), stage); err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if ref.Epoch != e.state.epoch {
		return Result{}, ErrCatalogReplaced
	}
	// Check all originals before transferring any member. Earlier overlay originals
	// remain intact across repeated renames and savepoint rollback.
	for a := range stage.descriptors {
		if e.state.descriptors[a] != grantCatalog.descriptors[a] {
			return Result{}, CatalogConflict{}
		}
	}
	for k := range stage.touched {
		if e.state.names[k] != grantCatalog.names[k] {
			return Result{}, CatalogConflict{}
		}
	}
	for a, d := range stage.descriptors {
		e.catalogChangeLocked(changes, a, d)
	}
	scope.catalog = changes
	return Result{}, nil
}

func renameLocalConstraint(parent *relationDescriptor, old, destination string, kind byte) (*relationDescriptor, error) {
	collision := func(name string) error {
		if name == destination {
			return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, destination, parent.name))
		}
		return nil
	}
	for _, c := range parent.notNullConstraints {
		if err := collision(c.Name); err != nil {
			return nil, err
		}
	}
	for _, c := range parent.checkConstraints {
		if err := collision(c.Name); err != nil {
			return nil, err
		}
	}
	for _, c := range parent.uniqueConstraints {
		if err := collision(c.Name); err != nil {
			return nil, err
		}
	}
	for _, c := range parent.exclusionConstraints {
		if err := collision(c.Name); err != nil {
			return nil, err
		}
	}
	for _, c := range parent.foreignKeys {
		if err := collision(c.Name); err != nil {
			return nil, err
		}
	}
	if parent.revision == ids.DefinitionRevision(^uint64(0)) {
		return nil, fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next := *parent
	next.revision++
	if kind == 'n' {
		next.notNullConstraints = slices.Clone(parent.notNullConstraints)
		for i := range next.notNullConstraints {
			if next.notNullConstraints[i].Name == old {
				next.notNullConstraints[i].Name = destination
				return &next, nil
			}
		}
	} else if kind == 'f' {
		next.foreignKeys = slices.Clone(parent.foreignKeys)
		for i := range next.foreignKeys {
			if next.foreignKeys[i].Name == old {
				next.foreignKeys[i].Name = destination
				return &next, nil
			}
		}
	} else {
		next.checkConstraints = slices.Clone(parent.checkConstraints)
		for i := range next.checkConstraints {
			if next.checkConstraints[i].Name == old {
				next.checkConstraints[i].Name = destination
				slices.SortFunc(next.checkConstraints, func(a, b checkConstraint) int { return strings.Compare(a.Name, b.Name) })
				return &next, nil
			}
		}
	}
	return nil, ErrCatalogReplaced
}
