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
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	h := DDLHost{E: e, Op: scope.Op}
	old, ref, err := fdw.AlterTarget(h, auth, s, scope.LockedRef)
	if err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	if old == nil {
		e.Mu.Unlock()
		scope.Completed = true
		return Result{}, nil
	}
	if s.Owner != nil {
		target, identity := access.OwnershipRoleOperand(*s.Owner)
		e.Mu.Unlock()
		return e.executeRelationOwnership(scope, s.Name, target, identity, "foreign table", s.IfExists, 0)
	}
	if s.Inner != nil {
		switch inner := s.Inner.(type) {
		case *parser.AlterTableRenameTable:
			return e.renameRelationLocked(scope, scope.Op, auth, inner.Name, "", inner.NewName.Text, inner.IfExists, false, false, false, 0)
		case *parser.AlterTableRenameColumn:
			return e.renameRelationLocked(scope, scope.Op, auth, inner.Name, inner.Column.Text, inner.NewName.Text, inner.IfExists, true, false, false, 0)
		case *parser.AlterTableSetSchema:
			return e.alterSchemaLocked(scope, scope.Op, auth, inner, 0)
		}
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, scope.Op.Session)
	h.CatalogStage = stage
	wrapper, err := fdw.AlterBases(h, auth, old)
	e.Mu.Unlock()
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
			if err := scope.Op.Context().Err(); err != nil {
				return Result{}, err
			}
			if action.Options != nil {
				e.Mu.Lock()
				job, er := fdw.AlterOptions(h, &next, wrapper, action)
				e.Mu.Unlock()
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
					e.Mu.Lock()
					definition, er := e.cookAlterDefaultLocked(scope, scope.Op, auth, &next, slot, a.Expression)
					e.Mu.Unlock()
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
				candidate, err = table.NotNullCandidate(scope.Op, auth.Catalog, &next, a, table.NotNullDirect)
			case parser.AddCheck:
				candidate = &next
				candidate.Constraints.Check = slices.Clone(next.Constraints.Check)
				e.Mu.Lock()
				err = table.BindChecks(alterHost{h, scope}, auth, candidate, []*parser.CheckConstraint{a.Check}, scope.Input, true, nil)
				e.Mu.Unlock()
			case parser.DropColumn:
				candidate, err = scope.dropColumnCandidate(stage, &next, ref, a, nil)
			case parser.AddColumn:
				if err = fdw.CheckAddedColumn(a.Definition); err != nil {
					return Result{}, err
				}
				var added table.AddedColumn
				candidate, added, err = e.addColumnCandidate(scope, scope.Op, auth, &next, a, nil)
				if err == nil && added.Column != nil {
					if added.Declaration.NotNull {
						candidate, err = table.NotNullCandidate(scope.Op, auth.Catalog, candidate, parser.AlterTableAction{Action: parser.SetNotNull, Column: a.Column}, table.NotNullDirect)
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
							e.Mu.Lock()
							err = table.BindChecks(alterHost{h, scope}, auth, candidate, checks, scope.Input, true, nil)
							e.Mu.Unlock()
						}
					}
					if err == nil {
						e.Mu.Lock()
						job, er := fdw.AddedColumnOptions(h, candidate, wrapper, a.Column.Text, action.ColumnOptions)
						e.Mu.Unlock()
						if er != nil {
							return Result{}, er
						}
						jobs = append(jobs, job)
					}
				}
			case parser.AlterColumnType:
				e.Mu.Lock()
				prepared, prepareErr := e.prepareAlterTypeLocked(scope, scope.Op, auth, old, ref, a, nil)
				e.Mu.Unlock()
				if prepareErr != nil {
					return Result{}, prepareErr
				}
				candidate, _, err = e.alterTypeCandidate(scope, scope.Op, auth, stage, old, &next, ref, a, prepared)
			case parser.SetExpression, parser.DropExpression:
				candidate, _, err = e.generationCandidate(scope, scope.Op, auth, &next, a)
			default:
				return Result{}, fdw.UnsupportedAlter()
			}
			if err != nil {
				return Result{}, err
			}
			next = *candidate
		}
		if pass == 0 {
			pendingChecks, err = table.CaptureAlterTypeChecks(scope.Op, auth.Catalog, old, &next, typeActions)
		}
		if err == nil && pass == 1 {
			var rebound *catalog.Relation
			rebound, _, err = e.rebindAlterTypeChecks(scope, scope.Op, auth, ref, &next, pendingChecks)
			if err == nil {
				next = *rebound
			}
		}
		if err != nil {
			return Result{}, err
		}
	}
	e.Mu.Lock()
	next.ObjectRevision, err = e.Catalog.RevisionLocked()
	if err == nil {
		err = table.AssignDefinitionOIDs(DDLHost{E: e}, &next)
	}
	e.Mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if err = stage.Replace(&next); err != nil {
		return Result{}, err
	}
	if err = scope.AcquireDefinitionProviders(stage); err != nil {
		return Result{}, err
	}
	if scope.ReplacementRead {
		e.ReplacementMu.RUnlock()
		scope.ReplacementRead = false
	}
	if err = fdw.RunValidations(scope.Op.Context(), jobs); err != nil {
		return Result{}, err
	}
	prepared, err := e.ValidateCatalogStage(scope.Op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	_, err = e.PublishCatalogOperation(scope.Op, scope, prepared, PublicationToken{Epoch: ref.Epoch})
	return Result{}, err
}
