package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Sequence AS uses admitted target usages, including the prefix retained when
// transformation completion fails. It never visits an unadmitted later TYPE.
func (e *Engine) stageAlterIdentityTypes(scope *StatementScope, op *session.Operation, auth AuthorizationView, original *catalog.Relation, actions []parser.AlterTableAction, prepared []table.AlterTypePreparation) (*CatalogStage, error) {
	var stage *CatalogStage
	index := 0
	for _, action := range actions {
		if action.Action != parser.AlterColumnType {
			continue
		}
		if index == len(prepared) {
			break
		}
		slot := original.Index[action.Column.Text]
		attribute := original.Attributes[slot]
		if identity, exists := original.ColumnMeta.Identities[attribute]; exists {
			if stage == nil {
				if scope.inheritance.alter != nil {
					stage = scope.inheritance.alter.stage
				} else {
					stage = e.BeginCatalogStage(auth.Catalog)
					stage.authorizeLocked(auth, op.Session)
				}
			}
			if err := e.alterIdentityTypeSequence(scope, stage, original, attribute, identity.Sequence, prepared[index].Usage); err != nil {
				return stage, err
			}
		}
		index++
	}
	return stage, nil
}

func alterTypeNamespace(auth AuthorizationView, original *catalog.Relation, ref ids.ObjectRef) *analyze.QueryNamespace {
	columns := make([]analyze.QueryColumn, len(original.Columns))
	for i, column := range original.Columns {
		columns[i] = analyze.QueryColumn{Descriptor: column, Slot: i, Leaf: analyze.QueryTargetLeaf, Physical: i}
	}
	qualifier := analyze.QueryQualifier{Name: original.Name(), Original: original.Name(), Schema: auth.Catalog.Get(original.Namespace()).Name(), Ref: ref, Columns: columns}
	return &analyze.QueryNamespace{Columns: columns, Qualifiers: []analyze.QueryQualifier{qualifier}, Frame: original.Columns}
}

// prepareAlterTypeLocked admits and completes one TYPE action; the caller holds
// Engine.mu. suppliedUsage is used only by typed propagation of an already
// resolved type.
func (e *Engine) prepareAlterTypeLocked(scope *StatementScope, op *session.Operation, auth AuthorizationView, original *catalog.Relation, ref ids.ObjectRef, a parser.AlterTableAction, suppliedUsage *sqltype.Usage) (table.AlterTypePreparation, error) {
	return table.PrepareAlterType(alterHost{ddlHost{e: e, op: op}, scope}, auth, original, ref, a, suppliedUsage)
}

// alterTypeCandidate runs the TYPE candidate under Engine.mu; stage, when
// begun, supplies the staged dependent views.
func (e *Engine) alterTypeCandidate(scope *StatementScope, op *session.Operation, auth AuthorizationView, stage *CatalogStage, original, next *catalog.Relation, ref ids.ObjectRef, a parser.AlterTableAction, prepared table.AlterTypePreparation) (*catalog.Relation, index.ColumnTransformation, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return table.AlterTypeCandidate(alterHost{ddlHost{e: e, op: op, stage: stage}, scope}, auth, original, next, ref, a, prepared)
}

// Rewrite only effective live heads. Own updates become inserts into the private
// replacement because their committed predecessors are intentionally omitted.
func rewriteTypeRows(scope *StatementScope, op *session.Operation, old, next *catalog.Relation, store *storage.Relation, frontier ids.CommitSeq, transforms []index.ColumnTransformation, additions []table.AddedColumn, generations []int32, validator table.AlterRowValidator) (*storage.Relation, []storage.RowVersion, *visibility.PrivateDeletes, error) {
	if store.Generation == ^uint64(0) {
		return nil, nil, nil, fmt.Errorf("stuntgres: storage generation exhausted")
	}
	slots := make([]int, len(next.Attributes))
	destinations := make([]int, len(transforms))
	addedSlots := make([]struct {
		index  int
		perRow bool
	}, len(additions))
	generatedSlots := make([]int, len(generations))
	for i, attribute := range generations {
		generatedSlots[i] = next.IndexForAttribute(attribute)
	}
	for i, added := range additions {
		addedSlots[i].index = next.Index[added.Column.Name]
		if added.Expression != nil {
			properties := added.Expression.Properties()
			addedSlots[i].perRow = properties.Volatility > expr.Stable || properties.RuntimeDomainConversion
		}
	}
	for i, attribute := range next.Attributes {
		slots[i] = -1
		for j, id := range old.Attributes {
			if id == attribute {
				slots[i] = j
				break
			}
		}
	}
	for i, transform := range transforms {
		for slot, attribute := range next.Attributes {
			if transform.Attribute == attribute {
				destinations[i] = slot
				slots[slot] = -1
				break
			}
		}
	}
	writes, deletes := scope.columnProjectionRoots(old.Address())
	var replaced map[ids.RowID]bool
	for _, row := range writes {
		if row.Base != 0 {
			if replaced == nil {
				replaced = make(map[ids.RowID]bool)
			}
			replaced[row.Row] = true
		}
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Host: hostOf(scope), Input: scope.input, Output: scope.output, Catalog: scope.routineAuth.Catalog}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	preparedTransforms := false
	var generationProgram []expr.Expr
	preparedGeneration := false
	convert := func(input []storage.RowVersion, committed bool) ([]storage.RowVersion, error) {
		result := make([]storage.RowVersion, 0, len(input))
		for _, row := range input {
			if err := op.Context().Err(); err != nil {
				return nil, err
			}
			if committed && replaced[row.Row] || !visibility.LiveVersion(row, frontier, store.Ends, deletes) {
				continue
			}
			evaluation.Row = row.Values
			if !preparedTransforms {
				var private []index.ColumnTransformation
				for i, transform := range transforms {
					recipe := evaluation
					recipe.RoutineRecipe, recipe.RoutineGraft = true, false
					recipe.RoutineChecks = transform.Dependencies
					node, changed, err := exec.PreflightCoercionsWithPolicy(&recipe, transform.Expression, true)
					evaluation.PreparedDomainChecks = recipe.PreparedDomainChecks
					if err != nil {
						return nil, err
					}
					if err := exec.AdmitExpression(&recipe, node, recipe.Catalog); err != nil {
						return nil, err
					}
					if changed {
						if private == nil {
							private = append([]index.ColumnTransformation(nil), transforms...)
						}
						private[i].Expression = node
					}
				}
				if private != nil {
					transforms = private
				}
				preparedTransforms = true
			}
			values := make([]sqltype.Value, len(slots))
			for i, slot := range slots {
				if slot >= 0 {
					values[i] = row.Values[slot]
				}
			}
			for i, transform := range transforms {
				// Lazy SQL bodies still need this transformation's original
				// manifest after preparation has returned.
				recipe := evaluation
				recipe.RoutineRecipe, recipe.RoutineGraft = true, false
				recipe.RoutineChecks = transform.Dependencies
				v, err := transform.Expression.Eval(&recipe)
				evaluation.PreparedDomainChecks = recipe.PreparedDomainChecks
				if err != nil {
					return nil, err
				}
				values[destinations[i]] = v
			}
			for i, added := range additions {
				value := added.Value
				if addedSlots[i].perRow {
					var err error
					value, err = added.Expression.Eval(&evaluation)
					if err != nil {
						return nil, err
					}
				}
				values[addedSlots[i].index] = value
			}
			evaluation.Row = values
			if !preparedGeneration && len(generations) != 0 {
				var err error
				generationProgram, err = table.PrepareGenerationProgram(&evaluation, next, generatedSlots, old)
				if err != nil {
					return nil, err
				}
				preparedGeneration = true
			}
			for i, attribute := range generations {
				expression := table.GenerationRecipeExpression(next.ColumnMeta.Generations[attribute])
				if generationProgram != nil {
					expression = generationProgram[generatedSlots[i]]
				}
				value, err := expression.Eval(&evaluation)
				if err != nil {
					return nil, err
				}
				values[generatedSlots[i]] = value
			}
			if err := validator.Validate(values, &evaluation); err != nil {
				return nil, err
			}
			row.Values = values
			row.IndexOutputs = nil
			if !committed {
				row.Base = 0
			}
			result = append(result, row)
		}
		return result, nil
	}
	committed, err := convert(store.Versions, true)
	if err != nil {
		return nil, nil, nil, err
	}
	own, err := convert(writes, false)
	if err != nil {
		return nil, nil, nil, err
	}
	return &storage.Relation{Versions: committed, Generation: store.Generation + 1}, own, nil, nil
}
