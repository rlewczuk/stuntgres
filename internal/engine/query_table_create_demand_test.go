package engine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func queryCreateBoundSource(t *testing.T, e *Engine, width int) (*plan.ProjectNode, *exec.Source) {
	t.Helper()
	schema := make(plan.QuerySchema, width)
	expressions := make([]expr.Expr, width)
	rows := make([][]sqltype.Value, 3)
	for i := range rows {
		rows[i] = make([]sqltype.Value, width)
	}
	for i := range schema {
		usage := testUsage(e, sqltype.Int4)
		schema[i] = catalog.Column{Name: "i", Usage: usage}
		expressions[i] = plan.ColumnExpr{Info: expr.Info{Usage: usage, Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1, Slot: i}
		for j := range rows {
			rows[j][i], _ = sqltype.Integer(usage.Type, int64(j+1))
		}
	}
	source := &exec.Source{Anonymous: rows}
	return &plan.ProjectNode{QuerySchema: schema, Child: &plan.SourceNode{QuerySchema: schema}, Expressions: expressions, Frame: 1}, source
}
