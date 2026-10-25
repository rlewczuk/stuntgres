package engine

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// The owner-authorized AX grant protects identity; publication changes only the
// immutable descriptor and never evaluates a default or rewrites row storage.
// Every return releases Engine.mu.
func alterDefaultLocked(e *core.Engine, scope *core.StatementScope, op *session.Operation, auth core.AuthorizationView, s *parser.AlterTableDefault, count int) (core.Result, error) {
	fail := func(err error) (core.Result, error) { e.Mu.Unlock(); return core.Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	if s.Action != parser.SetDefault && s.Action != parser.DropDefault || s.Action == parser.SetDefault && s.Expression == nil || s.Action == parser.DropDefault && s.Expression != nil {
		return fail(fmt.Errorf("stuntgres: invalid default alteration"))
	}
	ref, err := e.ResolveReference(auth, s.Name)
	if err == nil && ref != scope.LockedRef {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		err = auth.Check(access.Requirement{Object: ref, OwnerOnly: true})
	}
	if err != nil {
		return fail(err)
	}
	old := scope.LockedDescriptor.(*catalog.Relation)
	if catalog.HierarchyRelation(old) && !core.PartitionDefaultReplayAllowed(e, op, auth, s) {
		return fail(core.SQLError("0A000", "altering partition hierarchies is unsupported"))
	}
	slot, err := table.AlterDefaultColumn(old, s)
	if err != nil {
		return fail(err)
	}
	attr := old.Attributes[slot]
	if catalog.InheritanceParticipant(auth.Catalog, old) {
		return alterInheritanceDefaultLocked(e, scope, op, auth, s, old)
	}
	var definition catalog.ColumnDefault
	if s.Action == parser.SetDefault {
		definition, err = cookAlterDefaultLocked(e, scope, op, auth, old, slot, s.Expression)
		if err != nil {
			return fail(err)
		}
	} else if _, present := old.ColumnMeta.Defaults[attr]; !present {
		op.Session.Mu.Lock()
		err = core.PublicationOperationLocked(e, op)
		op.Session.Mu.Unlock()
		if err == nil {
			err = op.Context().Err()
		}
		if err == nil {
			scope.Completed = true
		}
		e.Mu.Unlock()
		return core.Result{}, err
	}
	next, err := table.AlterDefaultCandidate(old, attr, definition)
	if err != nil {
		return fail(err)
	}
	original := auth.StorageLocked(ref.Address)
	if original == nil && old.Partition == nil {
		return fail(fmt.Errorf("stuntgres: relation storage is missing"))
	}
	var length int
	var generation uint64
	var ends *storage.DeletionRoot
	if original != nil {
		length, generation, ends = len(original.Versions), original.Generation, original.Ends
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if original == nil {
		err = stage.Replace(next)
	} else {
		err = stage.AlterRelation(op.Context(), ref, next, original, original, length, generation, ends)
	}
	if err != nil {
		return core.Result{}, err
	}
	if err := scope.AcquireDefinitionProviders(stage); err != nil {
		return core.Result{}, err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return core.Result{}, err
	}
	if _, err = e.PublishCatalogOperation(op, scope, prepared, core.PublicationToken{Epoch: ref.Epoch}); err != nil {
		return core.Result{}, err
	}
	scope.Completed = true
	return core.Result{}, nil
}

// cookAlterDefaultLocked cooks SET DEFAULT; the caller holds Engine.mu.
func cookAlterDefaultLocked(e *core.Engine, scope *core.StatementScope, op *session.Operation, auth core.AuthorizationView, old *catalog.Relation, slot int, expression parser.Expr) (catalog.ColumnDefault, error) {
	return table.CookAlterDefault(alterHost{TableHost{core.DDLHost{E: e, Op: op}}, scope}, auth, old, slot, expression)
}
