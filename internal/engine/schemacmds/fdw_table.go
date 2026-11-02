package schemacmds

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
)

func executeForeignTableAlter(e *core.Engine, scope *core.StatementScope, s *parser.ForeignDDL) (core.Result, error) {
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		e.Mu.Unlock()
		return core.Result{}, err
	}
	h := tablecmds.TableHost{core.DDLHost{E: e, Op: scope.Op}}
	old, ref, err := fdw.AlterTarget(h, auth, s, scope.LockedRef)
	if err != nil {
		e.Mu.Unlock()
		return core.Result{}, err
	}
	if old == nil {
		e.Mu.Unlock()
		scope.CompleteUtility()
		return core.Result{}, nil
	}
	if s.Owner != nil {
		target, identity := access.OwnershipRoleOperand(*s.Owner)
		e.Mu.Unlock()
		return core.ExecuteRelationOwnership(e, scope, s.Name, target, identity, "foreign table", s.IfExists, 0)
	}
	if s.Inner != nil {
		switch inner := s.Inner.(type) {
		case *parser.AlterTableRenameTable:
			return tablecmds.RenameRelationLocked(e, scope, scope.Op, auth, inner.Name, "", inner.NewName.Text, inner.IfExists, false, false, false, 0)
		case *parser.AlterTableRenameColumn:
			return tablecmds.RenameRelationLocked(e, scope, scope.Op, auth, inner.Name, inner.Column.Text, inner.NewName.Text, inner.IfExists, true, false, false, 0)
		case *parser.AlterTableSetSchema:
			return tablecmds.AlterSchemaLocked(e, scope, scope.Op, auth, inner, 0)
		}
	}
	var ordinary []parser.AlterTableAction
	var remaining []parser.ForeignTableAction
	var addedColumns map[string]bool
	for _, action := range s.TableActions {
		if action.Ordinary == nil {
			remaining = append(remaining, action)
			continue
		}
		a := action.Ordinary
		ordinary = append(ordinary, *a)
		if a.Action == parser.AddColumn {
			_, existed := old.Index[a.Column.Text]
			skipped := a.IfNotExists && (existed || addedColumns[a.Column.Text])
			if addedColumns == nil {
				addedColumns = make(map[string]bool)
			}
			addedColumns[a.Column.Text] = true
			if !skipped && len(action.ColumnOptions) != 0 {
				remaining = append(remaining, parser.ForeignTableAction{Column: a.Column, Options: action.ColumnOptions})
			}
		}
	}
	if len(ordinary) != 0 {
		command := &parser.AlterTable{Name: s.Name, IfExists: s.IfExists, Actions: ordinary, Pos: s.Pos}
		result, err := tablecmds.AlterTableLocked(e, scope, scope.Op, auth, command, 0, nil)
		if err != nil || len(remaining) == 0 {
			return result, err
		}
		tail := *s
		tail.TableActions = remaining
		return executeForeignTableAlter(e, scope, &tail)
	}
	for i, action := range s.TableActions {
		if action.Default == nil {
			continue
		}
		command := *action.Default
		command.Name = s.Name
		result, err := tablecmds.AlterDefaultLocked(e, scope, scope.Op, auth, &command, 0)
		if err != nil || len(s.TableActions) == 1 {
			return result, err
		}
		tail := *s
		tail.TableActions = make([]parser.ForeignTableAction, 0, len(s.TableActions)-1)
		tail.TableActions = append(tail.TableActions, s.TableActions[:i]...)
		tail.TableActions = append(tail.TableActions, s.TableActions[i+1:]...)
		return executeForeignTableAlter(e, scope, &tail)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, scope.Op.Session)
	h.CatalogStage = stage
	wrapper, err := fdw.AlterBases(h, auth, old)
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err != nil {
		return core.Result{}, err
	}
	next := fdw.AlterCandidate(old)
	jobs := make([]fdw.Validation, 0, len(s.TableActions))
	for _, action := range s.TableActions {
		if err := scope.Op.Context().Err(); err != nil {
			return core.Result{}, err
		}
		e.Mu.Lock()
		job, err := fdw.AlterOptions(h, &next, wrapper, action)
		e.Mu.Unlock()
		if err != nil {
			return core.Result{}, err
		}
		jobs = append(jobs, job)
	}
	e.Mu.Lock()
	next.ObjectRevision, err = e.Catalog.RevisionLocked()
	e.Mu.Unlock()
	if err != nil {
		return core.Result{}, err
	}
	if err = stage.Replace(&next); err != nil {
		return core.Result{}, err
	}
	if err = scope.AcquireDefinitionProviders(stage); err != nil {
		return core.Result{}, err
	}
	if scope.ReplacementRead {
		e.ReplacementMu.RUnlock()
		scope.ReplacementRead = false
	}
	if err = fdw.RunValidations(scope.Op.Context(), scope, jobs); err != nil {
		return core.Result{}, err
	}
	prepared, err := e.ValidateCatalogStage(scope.Op.Context(), stage)
	if err != nil {
		return core.Result{}, err
	}
	stage.RecordDDLCommand("ALTER FOREIGN TABLE", ref.Address)
	_, err = e.PublishCatalogOperation(scope.Op, scope, prepared, core.PublicationToken{Epoch: ref.Epoch})
	return core.Result{}, err
}
