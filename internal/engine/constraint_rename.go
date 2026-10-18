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
		return fail(ids.ErrCatalogReplaced)
	}
	if err := auth.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
		return fail(err)
	}
	parent := scope.lockedDescriptor.(*relationDescriptor)
	if parent.RelationKind == sequenceRelation {
		return fail(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot rename columns of relation "%s"`, parent.Name()), Detail: "This operation is not supported for sequences."})
	}
	// PostgreSQL accepts view/index targets here, then performs constraint lookup.
	var backing ids.ObjectAddress
	kind := byte(0)
	for _, c := range parent.NotNullConstraints {
		if c.Name == raw.Constraint.Text {
			kind = 'n'
			break
		}
	}
	for _, c := range parent.CheckConstraints {
		if c.Name == raw.Constraint.Text {
			kind = 'c'
			break
		}
	}
	for _, c := range parent.UniqueConstraints {
		if c.Name == raw.Constraint.Text {
			kind = 'u'
			backing = c.Index
			break
		}
	}
	for _, c := range parent.ExclusionConstraints {
		if c.Name == raw.Constraint.Text {
			kind = 'x'
			backing = c.Index
			break
		}
	}
	for _, c := range parent.ForeignKeys {
		if c.Name == raw.Constraint.Text {
			kind = 'f'
			break
		}
	}
	if kind == 0 {
		return fail(sqlError("42704", fmt.Sprintf(`constraint "%s" for table "%s" does not exist`, raw.Constraint.Text, parent.Name())))
	}
	if (kind == 'n' || kind == 'c') && inheritanceParticipant(auth.catalog, parent) {
		return e.renameInheritanceConstraintLocked(scope, op, auth, raw, parent, kind)
	}
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authorizeLocked(auth, op.session)
	if backing != (ids.ObjectAddress{}) {
		index, ok := auth.catalog.Relation(backing)
		if !ok {
			e.DiscardCatalogStage(stage)
			return fail(ids.ErrCatalogReplaced)
		}
		if _, exists := auth.catalog.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: parent.Namespace(), Name: raw.NewName.Text}); exists {
			e.DiscardCatalogStage(stage)
			return fail(sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, raw.NewName.Text)))
		}
		if index.Revision() == ids.DefinitionRevision(^uint64(0)) {
			e.DiscardCatalogStage(stage)
			return fail(fmt.Errorf("stuntgres: definition revision exhausted"))
		}
		next := *index
		next.ObjectName = raw.NewName.Text
		next.ObjectRevision++
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
	grantCatalog := e.catalog.state
	scope.catalogAuthority = auth.catalogAuthorityLocked(ref, grantCatalog.Get(ref.Address))
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if _, err := e.ValidateCatalogStage(op.Context(), stage); err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if ref.Epoch != e.catalog.state.Epoch() {
		return Result{}, ids.ErrCatalogReplaced
	}
	// Check all originals before transferring any member. Earlier overlay originals
	// remain intact across repeated renames and savepoint rollback.
	for a := range stage.descriptors {
		if e.catalog.state.Get(a) != grantCatalog.Get(a) {
			return Result{}, CatalogConflict{}
		}
	}
	for k := range stage.touched {
		if e.catalog.state.NameAddress(k) != grantCatalog.NameAddress(k) {
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
			return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, destination, parent.Name()))
		}
		return nil
	}
	for _, c := range parent.NotNullConstraints {
		if err := collision(c.Name); err != nil {
			return nil, err
		}
	}
	for _, c := range parent.CheckConstraints {
		if err := collision(c.Name); err != nil {
			return nil, err
		}
	}
	for _, c := range parent.UniqueConstraints {
		if err := collision(c.Name); err != nil {
			return nil, err
		}
	}
	for _, c := range parent.ExclusionConstraints {
		if err := collision(c.Name); err != nil {
			return nil, err
		}
	}
	for _, c := range parent.ForeignKeys {
		if err := collision(c.Name); err != nil {
			return nil, err
		}
	}
	if parent.Revision() == ids.DefinitionRevision(^uint64(0)) {
		return nil, fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next := *parent
	next.ObjectRevision++
	if kind == 'n' {
		next.NotNullConstraints = slices.Clone(parent.NotNullConstraints)
		for i := range next.NotNullConstraints {
			if next.NotNullConstraints[i].Name == old {
				next.NotNullConstraints[i].Name = destination
				return &next, nil
			}
		}
	} else if kind == 'f' {
		next.ForeignKeys = slices.Clone(parent.ForeignKeys)
		for i := range next.ForeignKeys {
			if next.ForeignKeys[i].Name == old {
				next.ForeignKeys[i].Name = destination
				return &next, nil
			}
		}
	} else {
		next.CheckConstraints = slices.Clone(parent.CheckConstraints)
		for i := range next.CheckConstraints {
			if next.CheckConstraints[i].Name == old {
				next.CheckConstraints[i].Name = destination
				slices.SortFunc(next.CheckConstraints, func(a, b checkConstraint) int { return strings.Compare(a.Name, b.Name) })
				return &next, nil
			}
		}
	}
	return nil, ids.ErrCatalogReplaced
}
