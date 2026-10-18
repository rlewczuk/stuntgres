package engine

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestInsertSelectValuesOccurrenceEvaluation(t *testing.T) {
	for _, count := range []int{0, 1, 4} {
		e := New()
		run(t, e, "CREATE TABLE t(v INT)")
		ref := catalogRef(t, e, e.CatalogRead(), "t")
		e.mu.Lock()
		relation := e.catalog.state.MustRelation(ref.Address)
		e.mu.Unlock()
		usage := relation.Columns[0].Usage
		typ := usage.Type
		calls := make([]int, count)
		expressions := make([][]expr.Expr, count)
		for i := range expressions {
			index := i
			expressions[i] = []expr.Expr{queryProbe(usage, func(c *EvalContext) (sqltype.Value, error) {
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
		values := &valuesNode{querySchema: schema, rows: expressions}
		comparison, err := sqltype.BindComparison(typ, typ, typ.Usage().Collation, false)
		if err != nil {
			t.Fatal(err)
		}
		projection := columnExpression{Info: expr.Info{Usage: usage, Property: expr.Properties{RowDependent: true}}, scope: 1, frame: 1, slot: 0}
		sorted := &sortNode{querySchema: schema, child: values, keys: []sortKey{{expression: projection, slot: 0, cache: -1, comparison: comparison}}}
		query := &BoundQuery{Root: &projectNode{querySchema: schema, child: sorted, expressions: []expr.Expr{projection}}}
		source := &querySource{}
		rows, err := evaluateInsertQuery(context.Background(), binding{relation: relation, ref: ref, targets: []int{0}, query: query}, source, nil, "public", sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, nil)
		if err != nil || len(rows.values) != count || source.anonymous != nil {
			t.Fatal(rows, err, source.anonymous)
		}
		for i, call := range calls {
			if call != 1 {
				t.Fatal("VALUES occurrence reevaluated/deduplicated", i, call)
			}
		}
		for i, row := range rows.values {
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
