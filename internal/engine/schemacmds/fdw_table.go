package schemacmds

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
)

// AlterTableColumnOptionsLocked preserves ordinary ALTER admission and ADD
// semantics, then applies its column FDW options through the existing foreign
// executor. Both phases belong to one statement journal, so a kind/validator
// failure cannot publish the ordinary prefix. Called with Engine.Mu held;
// every path releases it through AlterTableLocked before foreign callbacks.
func AlterTableColumnOptionsLocked(e *core.Engine, scope *core.StatementScope, auth core.AuthorizationView, s *parser.AlterTable, count int) (core.Result, error) {
	if count != 0 {
		return tablecmds.AlterTableLocked(e, scope, scope.Op, auth, s, count, nil, nil)
	}
	hasOptions := false
	for _, action := range s.Actions {
		if action.Action == parser.AddColumn && action.Definition != nil && len(action.Definition.ForeignOptions) != 0 {
			hasOptions = true
			break
		}
	}
	if !hasOptions {
		return tablecmds.AlterTableLocked(e, scope, scope.Op, auth, s, count, nil, nil)
	}
	var admittedAdds []int
	result, err := tablecmds.AlterTableLocked(e, scope, scope.Op, auth, s, count, nil, &admittedAdds)
	if err != nil {
		return result, err
	}
	// Only the phased executor knows whether an ADD survived its current-shape
	// IF NOT EXISTS gate, including a DROP of the same name earlier in the pass.
	var options []parser.ForeignTableAction
	for _, actionIndex := range admittedAdds {
		action := s.Actions[actionIndex]
		if action.Definition != nil && len(action.Definition.ForeignOptions) != 0 {
			options = append(options, parser.ForeignTableAction{Column: action.Column, Options: action.Definition.ForeignOptions})
		}
	}
	if len(options) == 0 {
		return result, nil
	}
	result, err = executeForeignTableAlter(e, scope, &parser.ForeignDDL{
		Action: "alter", Kind: "table", Name: s.Name, IfExists: s.IfExists,
		TableActions: options, Pos: s.Pos,
	})
	if err == nil {
		scope.CompleteUtility()
	}
	return result, err
}

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
	hasColumnOptions := false
	for _, action := range s.TableActions {
		if action.Ordinary != nil {
			ordinary = append(ordinary, *action.Ordinary)
			hasColumnOptions = hasColumnOptions || len(action.ColumnOptions) != 0
		}
	}
	if len(ordinary) != 0 {
		command := &parser.AlterTable{Name: s.Name, IfExists: s.IfExists, Actions: ordinary, Pos: s.Pos}
		var admittedAdds []int
		var admittedSink *[]int
		if hasColumnOptions {
			admittedSink = &admittedAdds
		}
		result, err := tablecmds.AlterTableLocked(e, scope, scope.Op, auth, command, 0, nil, admittedSink)
		if err != nil {
			return result, err
		}
		var remaining []parser.ForeignTableAction
		ordinaryIndex, admittedIndex := 0, 0
		for _, action := range s.TableActions {
			if action.Ordinary == nil {
				remaining = append(remaining, action)
				continue
			}
			if admittedIndex < len(admittedAdds) && admittedAdds[admittedIndex] == ordinaryIndex {
				if len(action.ColumnOptions) != 0 {
					remaining = append(remaining, parser.ForeignTableAction{Column: action.Ordinary.Column, Options: action.ColumnOptions})
				}
				admittedIndex++
			}
			ordinaryIndex++
		}
		if len(remaining) == 0 {
			return result, nil
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
