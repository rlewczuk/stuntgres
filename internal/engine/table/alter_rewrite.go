package table

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// Rewrite only effective live heads. Own updates become inserts into the private
// replacement because their committed predecessors are intentionally omitted.
func RewriteTypeRows(evaluation exec.Context, old, next *catalog.Relation, store *storage.Relation, frontier ids.CommitSeq, writes []storage.RowVersion, deletes *visibility.PrivateDeletes, transforms []index.ColumnTransformation, additions []AddedColumn, generations []int32, validator AlterRowValidator) (*storage.Relation, []storage.RowVersion, *visibility.PrivateDeletes, error) {
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
	var replaced map[ids.RowID]bool
	for _, row := range writes {
		if row.Base != 0 {
			if replaced == nil {
				replaced = make(map[ids.RowID]bool)
			}
			replaced[row.Row] = true
		}
	}
	preparedTransforms := false
	var generationProgram []expr.Expr
	preparedGeneration := false
	convert := func(input []storage.RowVersion, committed bool) ([]storage.RowVersion, error) {
		result := make([]storage.RowVersion, 0, len(input))
		for _, row := range input {
			if err := evaluation.Context.Err(); err != nil {
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
				generationProgram, err = PrepareGenerationProgram(&evaluation, next, generatedSlots, old)
				if err != nil {
					return nil, err
				}
				preparedGeneration = true
			}
			for i, attribute := range generations {
				expression := GenerationRecipeExpression(next.ColumnMeta.Generations[attribute])
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
			row.PrivateHistory = nil // A genuine rewrite starts a new physical heap.
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
