package engine

import (
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func foreignTableDeclaration(s *parser.CreateTable) error {
	if len(s.Parents) != 0 || s.PartitionOf != nil || s.PartitionBy != nil {
		return sqlError("0A000", "foreign table hierarchies are unsupported in embedded mode")
	}
	for _, element := range s.Elements {
		if element.Unique != nil || element.ForeignKey != nil || element.Exclusion != nil {
			return sqlError("0A000", "unique, primary key, exclusion and foreign key constraints are unsupported on foreign tables")
		}
		if element.Column != nil {
			for _, constraint := range element.Column.Constraints {
				if constraint.Unique != nil || constraint.ForeignKey != nil || constraint.Identity != nil {
					return sqlError("0A000", "unique, primary key, foreign key and identity constraints are unsupported on foreign tables")
				}
			}
		}
	}
	return nil
}

// PostgreSQL accepts these LIKE options for foreign destinations but does not
// copy their physical/identity properties. Apply that policy before the common
// binder or stage can create an index, sequence or physical column metadata.
func foreignTableSyntax(table *parser.CreateTable) *parser.CreateTable {
	next := table
	for i, element := range table.Elements {
		if element.Like == nil {
			continue
		}
		if next == table {
			copy := *table
			copy.Elements = slices.Clone(table.Elements)
			next = &copy
		}
		like := *element.Like
		options := make([]parser.LikeOption, len(like.Options)+4)
		copy(options, like.Options)
		for j, name := range [...]string{"indexes", "identity", "storage", "compression"} {
			options[len(like.Options)+j] = parser.LikeOption{Name: name}
		}
		like.Options = options
		next.Elements[i].Like = &like
	}
	return next
}

func (e *Engine) stageForeignTableLocked(scope *StatementScope, auth AuthorizationView, stage *CatalogStage, s *parser.ForeignDDL) ([]foreignValidation, []ids.ObjectAddress, error) {
	if s.Action == "drop" {
		var roots []ids.ObjectAddress
		for _, name := range s.Names {
			ref, err := e.resolveRelationReference(auth, name, true)
			if err != nil {
				if s.IfExists && renameMissing(err) {
					schemaNotice(scope.op, "00000", fmt.Sprintf(`foreign table "%s" does not exist, skipping`, objectNameText(name)))
					continue
				}
				return nil, nil, err
			}
			d := auth.Catalog.MustRelation(ref.Address)
			if d.RelationKind != catalog.ForeignRelation {
				return nil, nil, sqlError("42809", fmt.Sprintf(`"%s" is not a foreign table`, d.Name()))
			}
			if err := auth.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
				return nil, nil, err
			}
			if err := stage.removeObjectLocked(ref, false); err != nil {
				return nil, nil, err
			}
			roots = append(roots, d.Address())
		}
		return nil, roots, nil
	}
	ns, err := e.creationNamespace(auth, s.Name)
	if err != nil {
		return nil, nil, err
	}
	if err = auth.CheckCreate(ns); err != nil {
		return nil, nil, creationError(err, s.Name)
	}
	key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns.Address, Name: s.Name.Parts[len(s.Name.Parts)-1].Text}
	if stage.LookupName(key) != (ids.ObjectAddress{}) {
		if s.IfNotExists {
			scope.op.existingRelationNotice(key.Name)
			return nil, nil, nil
		}
		return nil, nil, sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, key.Name))
	}
	server, err := findForeign(auth.Catalog, ids.ForeignServerClassID, s.Server.Text)
	if err != nil {
		return nil, nil, err
	}
	if err = auth.Check(access.Requirement{Object: foreignRef(auth, server), Privileges: authorization.UsagePrivilege}); err != nil {
		return nil, nil, err
	}
	if err = foreignTableDeclaration(s.Table); err != nil {
		return nil, nil, err
	}
	if err = e.schemaTableLocked(scope.op, auth, stage, foreignTableSyntax(s.Table), ns.Address, auth.State.CurrentUser, scope.input, nil); err != nil {
		return nil, nil, err
	}
	d, ok := stage.Get(stage.LookupName(key)).(*catalog.Relation)
	if !ok {
		return nil, nil, fmt.Errorf("stuntgres: missing staged foreign relation")
	}
	d.RelationKind = catalog.ForeignRelation
	d.Foreign = &catalog.ForeignTableDefinition{Server: server.Address(), Columns: make(map[int32][]catalog.ForeignOption)}
	d.Foreign.Options, err = transformForeignOptions(nil, s.Options)
	if err != nil {
		return nil, nil, err
	}
	wrapper, ok := auth.Catalog.Foreign(server.Wrapper)
	if !ok {
		return nil, nil, fmt.Errorf("stuntgres: missing foreign-data wrapper")
	}
	stage.domainBases = append(stage.domainBases, server, wrapper)
	job, err := foreignValidatorJob(stage, wrapper, catalog.ForeignTable, d.Foreign.Options)
	if err != nil {
		return nil, nil, err
	}
	jobs := []foreignValidation{job}
	for _, attribute := range d.Attributes {
		column := d.Columns[d.IndexForAttribute(attribute)]
		options, er := transformForeignOptions(nil, s.ColumnOptions[column.Name])
		if er != nil {
			return nil, nil, er
		}
		d.Foreign.Columns[attribute] = options
		job, er := foreignValidatorJob(stage, wrapper, catalog.ForeignAttribute, options)
		if er != nil {
			return nil, nil, er
		}
		jobs = append(jobs, job)
	}
	return jobs, nil, nil
}

func foreignAlterAdmission(s *parser.ForeignDDL) parser.Statement {
	if s.Inner != nil {
		return s.Inner
	}
	return &parser.AlterTable{Name: s.Name, IfExists: s.IfExists, Pos: s.Pos}
}

func (e *Engine) executeForeignTableAlter(scope *StatementScope, s *parser.ForeignDDL) (Result, error) {
	e.mu.Lock()
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	ref, err := e.resolveRelationReference(auth, s.Name, true)
	if err != nil {
		e.mu.Unlock()
		if s.IfExists && renameMissing(err) {
			schemaNotice(scope.op, "00000", fmt.Sprintf(`relation "%s" does not exist, skipping`, objectNameText(s.Name)))
			scope.completed = true
			return Result{}, nil
		}
		return Result{}, err
	}
	old := auth.Catalog.MustRelation(ref.Address)
	if old.RelationKind != catalog.ForeignRelation {
		e.mu.Unlock()
		return Result{}, sqlError("42809", fmt.Sprintf(`"%s" is not a foreign table`, old.Name()))
	}
	if ref != scope.lockedRef {
		e.mu.Unlock()
		return Result{}, ids.ErrCatalogReplaced
	}
	if err = auth.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
		e.mu.Unlock()
		return Result{}, err
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
	server, ok := auth.Catalog.Foreign(old.Foreign.Server)
	if !ok {
		e.mu.Unlock()
		return Result{}, ids.ErrCatalogReplaced
	}
	wrapper, ok := auth.Catalog.Foreign(server.Wrapper)
	if !ok {
		e.mu.Unlock()
		return Result{}, ids.ErrCatalogReplaced
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, scope.op.session)
	stage.domainBases = append(stage.domainBases, server, wrapper)
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	next := *old
	foreign := *old.Foreign
	foreign.Columns = maps.Clone(old.Foreign.Columns)
	next.Foreign = &foreign
	var jobs []foreignValidation
	var pendingChecks []alterTypeCheck
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
				if ordinary := alterActionPhase(action.Ordinary.Action); ordinary >= 0 {
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
				kind := catalog.ForeignTable
				options := next.Foreign.Options
				var attribute int32
				if action.Column.Text != "" {
					slot, ok := next.Index[action.Column.Text]
					if !ok {
						return Result{}, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, action.Column.Text, next.Name()))
					}
					attribute = next.Attributes[slot]
					options = next.Foreign.Columns[attribute]
					kind = catalog.ForeignAttribute
				}
				options, err = transformForeignOptions(options, action.Options)
				if err != nil {
					return Result{}, err
				}
				if attribute == 0 {
					next.Foreign.Options = options
				} else {
					next.Foreign.Columns[attribute] = options
				}
				job, er := foreignValidatorJob(stage, wrapper, kind, options)
				if er != nil {
					return Result{}, er
				}
				jobs = append(jobs, job)
				continue
			}
			if action.Default != nil {
				a := action.Default
				slot, ok := next.Index[a.Column.Text]
				if !ok {
					return Result{}, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, a.Column.Text, next.Name()))
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
				candidate, err = notNullCandidate(scope.op, auth.Catalog, &next, a, notNullDirect)
			case parser.AddCheck:
				candidate = &next
				candidate.Constraints.Check = slices.Clone(next.Constraints.Check)
				err = e.bindColumnChecks(scope.op, auth, []*parser.CheckConstraint{a.Check}, candidate, scope.input, true, nil)
			case parser.DropColumn:
				candidate, err = scope.dropColumnCandidate(stage, &next, ref, a, nil)
			case parser.AddColumn:
				declaration := &parser.CreateTable{Elements: []parser.TableElement{{Column: a.Definition}}}
				if err = foreignTableDeclaration(declaration); err != nil {
					return Result{}, err
				}
				var added addedColumn
				candidate, added, err = e.addColumnCandidate(scope, scope.op, auth, &next, a, nil)
				if err == nil && added.column != nil {
					if added.declaration.NotNull {
						candidate, err = notNullCandidate(scope.op, auth.Catalog, candidate, parser.AlterTableAction{Action: parser.SetNotNull, Column: a.Column}, notNullDirect)
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
							err = e.bindColumnChecks(scope.op, auth, checks, candidate, scope.input, true, nil)
						}
					}
					if err == nil {
						options, er := transformForeignOptions(nil, action.ColumnOptions)
						if er != nil {
							return Result{}, er
						}
						attribute := candidate.Attributes[candidate.Index[a.Column.Text]]
						candidate.Foreign.Columns[attribute] = options
						job, er := foreignValidatorJob(stage, wrapper, catalog.ForeignAttribute, options)
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
				return Result{}, sqlError("0A000", "foreign table action requires unsupported local storage or write support")
			}
			if err != nil {
				return Result{}, err
			}
			next = *candidate
		}
		if pass == 0 {
			pendingChecks, err = captureAlterTypeChecks(scope.op, auth.Catalog, old, &next, typeActions)
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
	next.ObjectRevision, err = e.catalog.revisionLocked()
	if err == nil {
		err = e.assignDefinitionOIDsLocked(&next)
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
	if err = runForeignValidations(scope.op.Context(), jobs); err != nil {
		return Result{}, err
	}
	prepared, err := e.ValidateCatalogStage(scope.op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	_, err = e.publishCatalogOperation(scope.op, scope, prepared, PublicationToken{Epoch: ref.Epoch})
	return Result{}, err
}

func foreignImportError(err error, name string) error {
	if d, ok := err.(*sqlstate.Error); ok {
		copy := *d
		if copy.Context != "" {
			copy.Context += "\n"
		}
		copy.Context += fmt.Sprintf(`importing foreign table "%s"`, name)
		return &copy
	}
	return err
}
