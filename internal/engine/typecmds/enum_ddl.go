package typecmds

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// ExecuteEnumDDL runs CREATE TYPE ... AS ENUM, CREATE TYPE ... AS RANGE and
// ALTER TYPE on an enum.
func ExecuteEnumDDL(e *core.Engine, scope *core.StatementScope, stmt parser.Statement, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	op := scope.Op
	var locked ids.ObjectRef
	if alter, ok := stmt.(*parser.AlterEnum); ok {
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(op)
		var ref ids.ObjectRef
		if err == nil {
			_, ref, err = typedef.ResolveAlterEnumLocked(core.DDLHost{E: e, Op: op}, auth, alter.Name)
		}
		e.Mu.Unlock()
		if err != nil {
			return core.Result{}, err
		}
		// New-label bytes are immutable: reject invalid input before entering
		// the type queue, after object/kind/owner precedence has been resolved.
		if len(alter.Label.Text) > 63 {
			d := core.SQLError("42602", fmt.Sprintf(`invalid enum label "%s"`, alter.Label.Text)).(*sqlstate.Error)
			d.Detail = "Labels must be 63 bytes or less."
			return core.Result{}, d
		}
		// Same manager/ticket/deadline authority as transactional sequence DDL;
		// resource is the actual type object, never a fabricated relation alias.
		if err := scope.AcquireSequence(ref, lock.TypeExclusive, true); err != nil {
			return core.Result{}, err
		}
		locked = ref
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return core.Result{}, err
	}
	changes := scope.PendingCatalog().Clone()
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	defer e.DiscardCatalogStage(stage)
	host := tablecmds.TypeHost{tablecmds.TableHost{core.DDLHost{E: e, Op: op, CatalogStage: stage}}}
	var created ids.ObjectRef
	if create, ok := stmt.(*parser.CreateEnum); ok {
		if err := typedef.StageCreateEnumLocked(host, auth, create); err != nil {
			return core.Result{}, err
		}
		scope.CatalogAuthority = auth.CatalogAuthorityLocked(ids.ObjectRef{}, nil)
		if len(scope.Owner.Savepoints) == 0 {
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
	} else if create, ok := stmt.(*parser.CreateRange); ok {
		if err := typedef.StageCreateRangeLocked(host, auth, create); err != nil {
			return core.Result{}, err
		}
		// Retain the constructor's actual fresh range identity for the same
		// bounded owner-pair protocol used by CREATE DOMAIN. Never resolve
		// a later object by name or substitute the companion/array identity.
		for address, descriptor := range stage.Descriptors() {
			if typ, ok := descriptor.(*catalog.Type); ok && typ.SQLType.IsRange() && stage.Base.Get(address) == nil {
				created = e.Catalog.Domain.Ref(stage.Base.Epoch(), address)
				break
			}
		}
		scope.CatalogAuthority = auth.CatalogAuthorityLocked(ids.ObjectRef{}, nil)
	} else {
		alter := stmt.(*parser.AlterEnum)
		old, ref, err := typedef.ResolveAlterEnumLocked(host, auth, alter.Name)
		if err != nil {
			return core.Result{}, err
		}
		if ref != locked {
			return core.Result{}, ids.ErrCatalogReplaced
		}
		scope.CatalogAuthority = auth.CatalogAuthorityLocked(ref, e.Catalog.State.Get(ref.Address))
		edit, err := typedef.StageAlterEnumLabelLocked(host, old, alter)
		if err != nil {
			return core.Result{}, err
		}
		if edit.Skipped {
			// AlterEnum returns the real type address even when AddEnumLabel
			// only emits its IF NOT EXISTS notice.
			scope.RecordDDLCommand("ALTER TYPE", ref.Address)
			return core.Result{}, nil
		}
		if edit.Added && (!typedef.EnumJournalOf(changes.Enum).CreatedTypes()[ref.Address] || len(scope.Owner.Savepoints) != 0) {
			journal := typedef.EnumWritable(changes)
			if journal.Unsafe == nil {
				journal.Unsafe = make(map[int64]bool)
			}
			journal.Unsafe[edit.Member] = true
		}
		// Existing private replacements must be the inputs to dependent reowning,
		// rather than the committed stores they already superseded.
		for a, shape := range changes.Storage {
			if stage.RowShapes == nil {
				stage.RowShapes = make(map[ids.ObjectAddress]storage.RowShape)
			}
			stage.RowShapes[a] = storage.RowShape{Original: shape.Original, Replacement: shape.Replacement, Generation: shape.Generation, ObservedEnds: shape.Ends, ObservedLength: len(shape.Root)}
		}
		if err := e.ReownStagedTypeGenerationsLocked(stage); err != nil {
			return core.Result{}, err
		}
		if err := adoptEnumGenerationsLocked(e, scope, changes, stage); err != nil {
			return core.Result{}, err
		}
	}
	for a, d := range stage.Descriptors() {
		e.CatalogChangeLocked(changes, a, d)
	}
	scope.Catalog = changes
	if scope.EventCommand != nil && scope.EventCommand.CollectDDL {
		scope.EventCommand.Commands = append(scope.EventCommand.Commands, stage.DDLCommands...)
	}
	return core.Result{Created: created}, nil
}
