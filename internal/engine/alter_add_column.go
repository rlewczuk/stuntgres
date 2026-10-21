package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The ADD COLUMN candidate runs under Engine.mu; DEFAULT recipes keep the
// statement's authority for row fills.
func (e *Engine) addColumnCandidate(scope *StatementScope, op *session.Operation, auth AuthorizationView, old *catalog.Relation, action parser.AlterTableAction, captured *sqltype.Usage) (*catalog.Relation, table.AddedColumn, error) {
	scope.routineAuth = auth
	e.mu.Lock()
	defer e.mu.Unlock()
	return table.AddColumnCandidate(alterHost{ddlHost{e: e, op: op}, scope}, auth, old, action, captured)
}

func planAddedColumns(scope *StatementScope, additions []table.AddedColumn) (map[int32]sqltype.Value, bool, error) {
	var fills map[int32]sqltype.Value
	perRow := false
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.op.Context(), Host: hostOf(scope), Input: scope.input, Output: scope.output, Catalog: scope.routineAuth.Catalog}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	for i := range additions {
		added := &additions[i]
		if added.Expression != nil {
			recipe := evaluation
			recipe.RoutineRecipe, recipe.RoutineGraft = true, false
			recipe.RoutineReferences, recipe.RoutineConsumer = added.Dependencies, added.Consumer
			planned, _, err := exec.PreflightCoercionsWithPolicy(&recipe, added.Expression, true)
			if err != nil {
				return nil, false, table.AlterCookingError(err)
			}
			if err := exec.AdmitExpression(&recipe, planned, recipe.Catalog); err != nil {
				return nil, false, table.AlterCookingError(err)
			}
			added.Expression = planned
			properties := planned.Properties()
			if properties.Volatility > expr.Stable || properties.RuntimeDomainConversion {
				perRow = true
				continue
			}
			added.Value, err = planned.Eval(&evaluation)
			if err != nil {
				return nil, false, table.AlterCookingError(err)
			}
		}
		if fills == nil {
			fills = make(map[int32]sqltype.Value, len(additions))
		}
		fills[added.Attribute] = added.Value
	}
	return fills, perRow, nil
}
