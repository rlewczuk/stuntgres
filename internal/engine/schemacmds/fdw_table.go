package schemacmds

import (
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/table"
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
	for pass := range 11 {
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
				return core.Result{}, err
			}
			if action.Options != nil {
				e.Mu.Lock()
				job, er := fdw.AlterOptions(h, &next, wrapper, action)
				e.Mu.Unlock()
				if er != nil {
					return core.Result{}, er
				}
				jobs = append(jobs, job)
				continue
			}
			if action.Default != nil {
				a := action.Default
				slot, er := fdw.AlterColumn(&next, a.Column.Text)
				if er != nil {
					return core.Result{}, er
				}
				next.ColumnMeta.Defaults = maps.Clone(next.ColumnMeta.Defaults)
				attribute := next.Attributes[slot]
				if a.Action == parser.DropDefault {
					delete(next.ColumnMeta.Defaults, attribute)
				} else {
					e.Mu.Lock()
					definition, er := tablecmds.CookAlterDefaultLocked(e, scope, scope.Op, auth, &next, slot, a.Expression)
					e.Mu.Unlock()
					if er != nil {
						return core.Result{}, er
					}
					if next.ColumnMeta.Defaults == nil {
						next.ColumnMeta.Defaults = make(map[int32]catalog.ColumnDefault)
					}
					next.ColumnMeta.Defaults[attribute] = definition
				}
				continue
			}
			if action.Ordinary == nil {
				return core.Result{}, fmt.Errorf("stuntgres: empty foreign table action")
			}
			a := *action.Ordinary
			var candidate *catalog.Relation
			switch a.Action {
			case parser.SetStorage, parser.SetCompression:
				err = table.AlterTableActionKind(&next, a.Action)
				if err == nil {
					e.Mu.Lock()
					candidate, _, err = table.ColumnStorageCandidate(stage.StageRead(), auth, &next, a)
					e.Mu.Unlock()
				}
			case parser.SetNotNull, parser.DropNotNull, parser.AddNotNull, parser.DropConstraint:
				candidate, err = table.NotNullCandidate(scope.Op, auth.Catalog, &next, a, table.NotNullDirect)
			case parser.AddCheck:
				candidate = &next
				candidate.Constraints.Check = slices.Clone(next.Constraints.Check)
				e.Mu.Lock()
				err = table.BindChecks(tablecmds.AlterHost{h, scope}, auth, candidate, []*parser.CheckConstraint{a.Check}, scope.Input, true, nil)
				e.Mu.Unlock()
			case parser.DropColumn:
				candidate, err = tablecmds.DropColumnCandidate(scope, stage, &next, ref, a, nil)
			case parser.AddColumn:
				if err = fdw.CheckAddedColumn(a.Definition); err != nil {
					return core.Result{}, err
				}
				var added table.AddedColumn
				candidate, added, err = tablecmds.AddColumnCandidate(e, scope, scope.Op, auth, &next, a, nil)
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
							err = table.BindChecks(tablecmds.AlterHost{h, scope}, auth, candidate, checks, scope.Input, true, nil)
							e.Mu.Unlock()
						}
					}
					if err == nil {
						e.Mu.Lock()
						job, er := fdw.AddedColumnOptions(h, candidate, wrapper, a.Column.Text, action.ColumnOptions)
						e.Mu.Unlock()
						if er != nil {
							return core.Result{}, er
						}
						jobs = append(jobs, job)
					}
				}
			case parser.AlterColumnType:
				e.Mu.Lock()
				prepared, prepareErr := tablecmds.PrepareAlterTypeLocked(e, scope, scope.Op, auth, old, ref, a, nil)
				e.Mu.Unlock()
				if prepareErr != nil {
					return core.Result{}, prepareErr
				}
				candidate, _, err = tablecmds.AlterTypeCandidate(e, scope, scope.Op, auth, stage, old, &next, ref, a, prepared)
			case parser.SetExpression, parser.DropExpression:
				candidate, _, err = tablecmds.GenerationCandidate(e, scope, scope.Op, auth, &next, a)
			default:
				return core.Result{}, fdw.UnsupportedAlter()
			}
			if err != nil {
				return core.Result{}, err
			}
			next = *candidate
		}
		if pass == 0 {
			pendingChecks, err = table.CaptureAlterTypeChecks(scope.Op, auth.Catalog, old, &next, typeActions)
		}
		if err == nil && pass == 1 {
			var rebound *catalog.Relation
			rebound, _, err = tablecmds.RebindAlterTypeChecks(e, scope, scope.Op, auth, ref, &next, pendingChecks)
			if err == nil {
				next = *rebound
			}
		}
		if err != nil {
			return core.Result{}, err
		}
	}
	e.Mu.Lock()
	next.ObjectRevision, err = e.Catalog.RevisionLocked()
	if err == nil {
		err = table.AssignDefinitionOIDs(core.DDLHost{E: e}, &next)
	}
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
	_, err = e.PublishCatalogOperation(scope.Op, scope, prepared, core.PublicationToken{Epoch: ref.Epoch})
	return core.Result{}, err
}
