package engine

import (
	"context"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestInsertSelectValuesOccurrenceEvaluation(t *testing.T) {
	for _, count := range []int{0, 1, 4} {
		e := New()
		run(t, e, "CREATE TABLE t(v INT)")
		ref := catalogRef(t, e, e.CatalogRead(), "t")
		e.mu.Lock()
		relation := e.catalog.State.MustRelation(ref.Address)
		e.mu.Unlock()
		usage := relation.Columns[0].Usage
		typ := usage.Type
		calls := make([]int, count)
		expressions := make([][]expr.Expr, count)
		for i := range expressions {
			index := i
			expressions[i] = []expr.Expr{queryProbe(usage, func(c *exec.Context) (sqltype.Value, error) {
				calls[index]++
				if index == 3 {
					return sqltype.Null(typ), nil
				}
				value := int64(1)
				if index == 2 {
					value = 2
				}
				return sqltype.Integer(typ, value)
			})}
		}
		schema := []catalog.Column{{Name: "column1", Usage: usage}}
		values := &plan.ValuesNode{QuerySchema: schema, Rows: expressions}
		comparison, err := sqltype.BindComparison(typ, typ, typ.Usage().Collation, false)
		if err != nil {
			t.Fatal(err)
		}
		projection := plan.ColumnExpr{Info: expr.Info{Usage: usage, Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1, Slot: 0}
		sorted := &plan.SortNode{QuerySchema: schema, Child: values, Keys: []plan.SortKey{{Expression: projection, Slot: 0, Cache: -1, Comparison: comparison}}}
		query := &plan.Query{Root: &plan.ProjectNode{QuerySchema: schema, Child: sorted, Expressions: []expr.Expr{projection}}}
		source := &exec.Source{}
		rows, err := dml.EvaluateInsertQuery(context.Background(), analyze.Binding{Relation: relation, Ref: ref, Targets: []int{0}, Query: query}, source, nil, "public", sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, nil)
		if err != nil || len(rows.Values) != count || source.Anonymous != nil {
			t.Fatal(rows, err, source.Anonymous)
		}
		for i, call := range calls {
			if call != 1 {
				t.Fatal("VALUES occurrence reevaluated/deduplicated", i, call)
			}
		}
		for i, row := range rows.Values {
			if i == 3 {
				if !row[0].IsNull() {
					t.Fatal(row)
				}
				continue
			}
			want := int64(1)
			if i == 2 {
				want = 2
			}
			got, err := row[0].Integer()
			if err != nil || got != want {
				t.Fatal(row, err, want)
			}
		}
	}
}
