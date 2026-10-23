package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

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
			_, ref, err = typedef.ResolveAlterEnumLocked(ddlHost{e: e, op: op}, auth, alter.Name)
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
	host := ddlHost{e: e, op: op, stage: stage}
	if create, ok := stmt.(*parser.CreateEnum); ok {
		if err := typedef.StageCreateEnumLocked(host, auth, create); err != nil {
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
		old, ref, err := typedef.ResolveAlterEnumLocked(host, auth, alter.Name)
		if err != nil {
			return Result{}, err
		}
		if ref != locked {
			return Result{}, ids.ErrCatalogReplaced
		}
		scope.catalogAuthority = auth.catalogAuthorityLocked(ref, e.catalog.state.Get(ref.Address))
		edit, err := typedef.StageAlterEnumLabelLocked(host, old, alter)
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
