package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (e *Engine) resolveAlterEnumLocked(auth AuthorizationView, name parser.ObjectName) (*catalog.Type, ids.ObjectRef, error) {
	// Utility object lookup has no cursor position, unlike expression type input.
	lookupName := name
	if len(name.Parts) == 2 {
		parts := [2]parser.Identifier{name.Parts[0], name.Parts[1]}
		parts[0].Pos = parser.Position{}
		lookupName.Parts = parts[:]
	}
	path, err := e.lookupNamespaces(auth, lookupName, analyze.TypeLookup)
	if err != nil {
		return nil, ids.ObjectRef{}, err
	}
	for i := range path.Count() {
		found := auth.Catalog.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: path.At(i), Name: name.Parts[len(name.Parts)-1].Text})
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
				display = catalog.TypeDiagnosticNameAt(auth.Catalog, d, path)
			}
			return nil, ref, sqlError("42809", display+" is not an enum")
		}
		if err := auth.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
			return nil, ref, err
		}
		return d, ref, nil
	}
	return nil, ids.ObjectRef{}, sqlError("42704", fmt.Sprintf("type %q does not exist", catalog.ObjectNameText(name)))
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
	changes := scope.owner.Catalog.Clone()
	stage := e.BeginCatalogStage(auth.Catalog)
	defer e.DiscardCatalogStage(stage)
	if create, ok := stmt.(*parser.CreateEnum); ok {
		if err := typedef.StageCreateEnumLocked(ddlHost{e: e, op: op, stage: stage}, auth, create); err != nil {
			return Result{}, err
		}
		scope.catalogAuthority = auth.catalogAuthorityLocked(ids.ObjectRef{}, nil)
		if len(scope.owner.Savepoints) == 0 {
			journal := typedef.EnumWritable(changes)
			if journal.Created == nil {
				journal.Created = make(map[ids.ObjectAddress]bool)
			}
			for a, d := range stage.Descriptors() {
				if typ, ok := d.(*catalog.Type); ok && typ.SQLType.IsEnum() {
					journal.Created[a] = true
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
		edit, err := typedef.StageAlterEnumLabelLocked(ddlHost{e: e, op: op, stage: stage}, old, alter)
		if err != nil {
			return Result{}, err
		}
		if edit.Skipped {
			return Result{}, nil
		}
		if edit.Added && (!typedef.EnumJournalOf(changes.Enum).CreatedTypes()[ref.Address] || len(scope.owner.Savepoints) != 0) {
			journal := typedef.EnumWritable(changes)
			if journal.Unsafe == nil {
				journal.Unsafe = make(map[int64]bool)
			}
			journal.Unsafe[edit.Member] = true
		}
		// Existing private replacements must be the inputs to dependent reowning,
		// rather than the committed stores they already superseded.
		for a, shape := range changes.Storage {
			if stage.rowShapes == nil {
				stage.rowShapes = make(map[ids.ObjectAddress]catalogRowShape)
			}
			stage.rowShapes[a] = catalogRowShape{original: shape.Original, replacement: shape.Replacement, generation: shape.Generation, observedEnds: shape.Ends, observedLength: len(shape.Root)}
		}
		if err := e.reownStagedTypeGenerationsLocked(stage); err != nil {
			return Result{}, err
		}
		if err := e.adoptEnumGenerationsLocked(scope, changes, stage); err != nil {
			return Result{}, err
		}
	}
	for a, d := range stage.Descriptors() {
		e.catalogChangeLocked(changes, a, d)
	}
	scope.catalog = changes
	return Result{}, nil
}
