package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Members are an owned mutation copy. Renumber only when an interior midpoint
// collapses in binary32; existing datum identities are never rewritten.
func enumInsertionOrder(members []sqltype.EnumMember, at int) float32 {
	if len(members) == 0 {
		return 1
	}
	if at == 0 {
		return members[0].SortOrder - 1
	}
	if at == len(members) {
		return members[at-1].SortOrder + 1
	}
	left, right := members[at-1].SortOrder, members[at].SortOrder
	midpoint := float32((float64(left) + float64(right)) / 2)
	if midpoint == left || midpoint == right {
		for i := range members {
			members[i].SortOrder = float32(i + 1)
		}
		midpoint = float32((float64(members[at-1].SortOrder) + float64(members[at].SortOrder)) / 2)
	}
	return midpoint
}

func (e *Engine) resolveAlterEnumLocked(auth AuthorizationView, name parser.ObjectName) (*catalog.Type, ids.ObjectRef, error) {
	// Utility object lookup has no cursor position, unlike expression type input.
	lookupName := name
	if len(name.Parts) == 2 {
		parts := [2]parser.Identifier{name.Parts[0], name.Parts[1]}
		parts[0].Pos = parser.Position{}
		lookupName.Parts = parts[:]
	}
	path, err := e.lookupNamespaces(auth, lookupName, typeLookup)
	if err != nil {
		return nil, ids.ObjectRef{}, err
	}
	for i := range path.count() {
		found := auth.Catalog.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: path.at(i), Name: name.Parts[len(name.Parts)-1].Text})
		if len(found) == 0 {
			continue
		}
		d, ok := auth.Catalog.Type(found[0])
		if !ok {
			return nil, ids.ObjectRef{}, fmt.Errorf("stuntgres: invalid type descriptor")
		}
		ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), d.Address())
		if !d.SQLType.IsEnum() {
			display := d.SQLType.DiagnosticName()
			if d.SQLType.IsUserDefined() || d.SQLType.IsDomain() || d.SQLType.IsComposite() {
				path, err := e.referencePath(auth)
				if err != nil {
					return nil, ref, err
				}
				display = typeDiagnosticNameAt(auth.Catalog, d, path)
			}
			return nil, ref, sqlError("42809", display+" is not an enum")
		}
		if err := auth.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
			return nil, ref, err
		}
		return d, ref, nil
	}
	return nil, ids.ObjectRef{}, sqlError("42704", fmt.Sprintf("type %q does not exist", objectNameText(name)))
}

func (e *Engine) executeEnumDDL(scope *StatementScope, stmt parser.Statement, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	op := scope.op
	var locked ids.ObjectRef
	if alter, ok := stmt.(*parser.AlterEnum); ok {
		e.mu.Lock()
		auth, err := e.authorizationLocked(op)
		var ref ids.ObjectRef
		if err == nil {
			_, ref, err = e.resolveAlterEnumLocked(auth, alter.Name)
		}
		e.mu.Unlock()
		if err != nil {
			return Result{}, err
		}
		// New-label bytes are immutable: reject invalid input before entering
		// the type queue, after object/kind/owner precedence has been resolved.
		if len(alter.Label.Text) > 63 {
			d := sqlError("42602", fmt.Sprintf(`invalid enum label "%s"`, alter.Label.Text)).(*sqlstate.Error)
			d.Detail = "Labels must be 63 bytes or less."
			return Result{}, d
		}
		// Same manager/ticket/deadline authority as transactional sequence DDL;
		// resource is the actual type object, never a fabricated relation alias.
		if err := scope.acquireSequence(ref, lock.TypeExclusive, true); err != nil {
			return Result{}, err
		}
		locked = ref
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return Result{}, err
	}
	changes := cloneCatalogChanges(scope.owner.catalog)
	stage := e.BeginCatalogStage(auth.Catalog)
	defer e.DiscardCatalogStage(stage)
	if create, ok := stmt.(*parser.CreateEnum); ok {
		if err := e.stageCreateEnumLocked(op, auth, stage, create); err != nil {
			return Result{}, err
		}
		scope.catalogAuthority = auth.catalogAuthorityLocked(ids.ObjectRef{}, nil)
		if len(scope.owner.savepoints) == 0 {
			if changes.enumCreated == nil {
				changes.enumCreated = make(map[ids.ObjectAddress]bool)
			}
			for a, d := range stage.Descriptors {
				if typ, ok := d.(*catalog.Type); ok && typ.SQLType.IsEnum() {
					changes.enumCreated[a] = true
				}
			}
		}
	} else {
		alter := stmt.(*parser.AlterEnum)
		old, ref, err := e.resolveAlterEnumLocked(auth, alter.Name)
		if err != nil {
			return Result{}, err
		}
		if ref != locked {
			return Result{}, ids.ErrCatalogReplaced
		}
		scope.catalogAuthority = auth.catalogAuthorityLocked(ref, e.catalog.state.Get(ref.Address))
		members := old.SQLType.EnumMembers()
		oldIndex, newIndex := -1, -1
		for i, m := range members {
			if m.Label == alter.Old.Text {
				oldIndex = i
			}
			if m.Label == alter.Label.Text {
				newIndex = i
			}
		}
		if alter.Rename && oldIndex < 0 {
			return Result{}, sqlError("22023", fmt.Sprintf(`"%s" is not an existing enum label`, alter.Old.Text))
		}
		if newIndex >= 0 {
			if !alter.Rename && alter.IfNotExists {
				op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: fmt.Sprintf(`enum label "%s" already exists, skipping`, alter.Label.Text)})
				return Result{}, nil
			}
			return Result{}, sqlError("42710", fmt.Sprintf(`enum label "%s" already exists`, alter.Label.Text))
		}
		if alter.Rename {
			members[oldIndex].Label = alter.Label.Text
		} else {
			at := len(members)
			if alter.Neighbor.Pos.Character != 0 {
				at = -1
				for i, m := range members {
					if m.Label == alter.Neighbor.Text {
						at = i
						break
					}
				}
				if at < 0 {
					return Result{}, sqlError("22023", fmt.Sprintf(`"%s" is not an existing enum label`, alter.Neighbor.Text))
				}
				if !alter.Before {
					at++
				}
			}
			member, err := e.catalog.allocateEnumMemberLocked()
			if err != nil {
				return Result{}, err
			}
			order := enumInsertionOrder(members, at)
			members = slices.Insert(members, at, sqltype.EnumMember{ID: member, Label: alter.Label.Text, SortOrder: order})
			if !changes.enumCreated[ref.Address] || len(scope.owner.savepoints) != 0 {
				if changes.enumUnsafe == nil {
					changes.enumUnsafe = make(map[int64]bool)
				}
				changes.enumUnsafe[member] = true
			}
		}
		typ, err := sqltype.EnumWithMembers(old.SQLType.ID(), old.Name(), old.SQLType.SQLName(), members)
		if err != nil {
			return Result{}, err
		}
		revision, err := e.catalog.revisionLocked()
		if err != nil {
			return Result{}, err
		}
		next := *old
		next.SQLType = typ
		next.ObjectRevision = revision
		if err := stage.Replace(&next); err != nil {
			return Result{}, err
		}
		// Existing private replacements must be the inputs to dependent reowning,
		// rather than the committed stores they already superseded.
		for a, shape := range changes.storage {
			if stage.rowShapes == nil {
				stage.rowShapes = make(map[ids.ObjectAddress]catalogRowShape)
			}
			stage.rowShapes[a] = catalogRowShape{original: shape.original, replacement: shape.replacement, generation: shape.generation, observedEnds: shape.ends, observedLength: len(shape.root)}
		}
		if err := e.reownStagedTypeGenerationsLocked(stage); err != nil {
			return Result{}, err
		}
		if err := e.adoptEnumGenerationsLocked(scope, changes, stage); err != nil {
			return Result{}, err
		}
	}
	for a, d := range stage.Descriptors {
		e.catalogChangeLocked(changes, a, d)
	}
	scope.catalog = changes
	return Result{}, nil
}
