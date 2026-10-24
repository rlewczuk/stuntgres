package engine

import (
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func (e *Engine) executeForeignTableAlter(scope *StatementScope, s *parser.ForeignDDL) (Result, error) {
	e.mu.Lock()
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	h := ddlHost{e: e, op: scope.op}
	old, ref, err := fdw.AlterTarget(h, auth, s, scope.lockedRef)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if old == nil {
		e.mu.Unlock()
		scope.completed = true
		return Result{}, nil
	}
	if s.Owner != nil {
		target, identity := access.OwnershipRoleOperand(*s.Owner)
		e.mu.Unlock()
		return e.executeRelationOwnership(scope, s.Name, target, identity, "foreign table", s.IfExists, 0)
	}
	if s.Inner != nil {
		switch inner := s.Inner.(type) {
		case *parser.AlterTableRenameTable:
			return e.renameRelationLocked(scope, scope.op, auth, inner.Name, "", inner.NewName.Text, inner.IfExists, false, false, false, 0)
		case *parser.AlterTableRenameColumn:
			return e.renameRelationLocked(scope, scope.op, auth, inner.Name, inner.Column.Text, inner.NewName.Text, inner.IfExists, true, false, false, 0)
		case *parser.AlterTableSetSchema:
			return e.alterSchemaLocked(scope, scope.op, auth, inner, 0)
		}
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, scope.op.Session)
	h.stage = stage
	wrapper, err := fdw.AlterBases(h, auth, old)
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err != nil {
		return Result{}, err
	}
	next := fdw.AlterCandidate(old)
	var jobs []fdw.Validation
	var pendingChecks []table.AlterTypeCheck
	var typeActions []parser.AlterTableAction
	if len(old.Constraints.Check) != 0 {
		for _, action := range s.TableActions {
			if action.Ordinary != nil && action.Ordinary.Action == parser.AlterColumnType {
				typeActions = append(typeActions, *action.Ordinary)
			}
		}
	}
	for pass := range 10 {
		for _, action := range s.TableActions {
			phase := 9
			if action.Ordinary != nil {
				if ordinary := table.AlterActionPhase(action.Ordinary.Action); ordinary >= 0 {
					phase = ordinary
				}
			}
			if phase != pass {
				continue
			}
			if err := scope.op.Context().Err(); err != nil {
				return Result{}, err
			}
			if action.Options != nil {
				e.mu.Lock()
				job, er := fdw.AlterOptions(h, &next, wrapper, action)
				e.mu.Unlock()
				if er != nil {
					return Result{}, er
				}
				jobs = append(jobs, job)
				continue
			}
			if action.Default != nil {
				a := action.Default
				slot, er := fdw.AlterColumn(&next, a.Column.Text)
				if er != nil {
					return Result{}, er
				}
				next.ColumnMeta.Defaults = maps.Clone(next.ColumnMeta.Defaults)
				attribute := next.Attributes[slot]
				if a.Action == parser.DropDefault {
					delete(next.ColumnMeta.Defaults, attribute)
				} else {
					e.mu.Lock()
					definition, er := e.cookAlterDefaultLocked(scope, scope.op, auth, &next, slot, a.Expression)
					e.mu.Unlock()
					if er != nil {
						return Result{}, er
					}
					if next.ColumnMeta.Defaults == nil {
						next.ColumnMeta.Defaults = make(map[int32]catalog.ColumnDefault)
					}
					next.ColumnMeta.Defaults[attribute] = definition
				}
				continue
			}
			if action.Ordinary == nil {
				return Result{}, fmt.Errorf("stuntgres: empty foreign table action")
			}
			a := *action.Ordinary
			var candidate *catalog.Relation
			switch a.Action {
			case parser.SetNotNull, parser.DropNotNull, parser.AddNotNull, parser.DropConstraint:
				candidate, err = table.NotNullCandidate(scope.op, auth.Catalog, &next, a, table.NotNullDirect)
			case parser.AddCheck:
				candidate = &next
				candidate.Constraints.Check = slices.Clone(next.Constraints.Check)
				e.mu.Lock()
				err = table.BindChecks(alterHost{h, scope}, auth, candidate, []*parser.CheckConstraint{a.Check}, scope.input, true, nil)
				e.mu.Unlock()
			case parser.DropColumn:
				candidate, err = scope.dropColumnCandidate(stage, &next, ref, a, nil)
			case parser.AddColumn:
				if err = fdw.CheckAddedColumn(a.Definition); err != nil {
					return Result{}, err
				}
				var added table.AddedColumn
				candidate, added, err = e.addColumnCandidate(scope, scope.op, auth, &next, a, nil)
				if err == nil && added.Column != nil {
					if added.Declaration.NotNull {
						candidate, err = table.NotNullCandidate(scope.op, auth.Catalog, candidate, parser.AlterTableAction{Action: parser.SetNotNull, Column: a.Column}, table.NotNullDirect)
					}
					if err == nil {
						var checks []*parser.CheckConstraint
						for _, constraint := range a.Definition.Constraints {
							if constraint.Check != nil {
								checks = append(checks, constraint.Check)
							}
						}
						if len(checks) != 0 {
							candidate.Constraints.Check = slices.Clone(candidate.Constraints.Check)
							e.mu.Lock()
							err = table.BindChecks(alterHost{h, scope}, auth, candidate, checks, scope.input, true, nil)
							e.mu.Unlock()
						}
					}
					if err == nil {
						e.mu.Lock()
						job, er := fdw.AddedColumnOptions(h, candidate, wrapper, a.Column.Text, action.ColumnOptions)
						e.mu.Unlock()
						if er != nil {
							return Result{}, er
						}
						jobs = append(jobs, job)
					}
				}
			case parser.AlterColumnType:
				e.mu.Lock()
				prepared, prepareErr := e.prepareAlterTypeLocked(scope, scope.op, auth, old, ref, a, nil)
				e.mu.Unlock()
				if prepareErr != nil {
					return Result{}, prepareErr
				}
				candidate, _, err = e.alterTypeCandidate(scope, scope.op, auth, stage, old, &next, ref, a, prepared)
			case parser.SetExpression, parser.DropExpression:
				candidate, _, err = e.generationCandidate(scope, scope.op, auth, &next, a)
			default:
				return Result{}, fdw.UnsupportedAlter()
			}
			if err != nil {
				return Result{}, err
			}
			next = *candidate
		}
		if pass == 0 {
			pendingChecks, err = table.CaptureAlterTypeChecks(scope.op, auth.Catalog, old, &next, typeActions)
		}
		if err == nil && pass == 1 {
			var rebound *catalog.Relation
			rebound, _, err = e.rebindAlterTypeChecks(scope, scope.op, auth, ref, &next, pendingChecks)
			if err == nil {
				next = *rebound
			}
		}
		if err != nil {
			return Result{}, err
		}
	}
	e.mu.Lock()
	next.ObjectRevision, err = e.catalog.RevisionLocked()
	if err == nil {
		err = table.AssignDefinitionOIDs(ddlHost{e: e}, &next)
	}
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if err = stage.Replace(&next); err != nil {
		return Result{}, err
	}
	if err = scope.acquireDefinitionProviders(stage); err != nil {
		return Result{}, err
	}
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	if err = fdw.RunValidations(scope.op.Context(), jobs); err != nil {
		return Result{}, err
	}
	prepared, err := e.ValidateCatalogStage(scope.op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	_, err = e.publishCatalogOperation(scope.op, scope, prepared, PublicationToken{Epoch: ref.Epoch})
	return Result{}, err
}
