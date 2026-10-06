package engine

import (
	"context"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestInsertSelectValuesOccurrenceEvaluation(t *testing.T) {
	for _, count := range []int{0, 1, 4} {
		e := New()
		run(t, e, "CREATE TABLE t(v INT)")
		ref := catalogRef(t, e, e.CatalogRead(), "t")
		e.mu.Lock()
		relation := e.state.descriptors[ref.Address].(*relationDescriptor)
		e.mu.Unlock()
		usage := relation.columns[0].Usage
		typ := usage.Type
		calls := make([]int, count)
		expressions := make([][]BoundExpr, count)
		for i := range expressions {
			index := i
			expressions[i] = []BoundExpr{queryProbe(usage, func(c *EvalContext) (sqltype.Value, error) {
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
		schema := []ColumnDescriptor{{Name: "column1", Usage: usage}}
		values := &valuesNode{querySchema: schema, rows: expressions}
		comparison, err := sqltype.BindComparison(typ, typ)
		if err != nil {
			t.Fatal(err)
		}
		sorted := &sortNode{querySchema: schema, child: values, slot: 0, comparison: comparison}
		projection := columnExpression{expressionInfo: expressionInfo{usage: usage, properties: ExprProperties{RowDependent: true}}, scope: 1, frame: 1, slot: 0}
		query := &BoundQuery{Root: &projectNode{querySchema: schema, child: sorted, expressions: []BoundExpr{projection}}}
		source := &querySource{}
		rows, err := evaluateInsertQuery(context.Background(), binding{relation: relation, ref: ref, targets: []int{0}, query: query}, source, nil, "public")
		if err != nil || len(rows) != count || source.anonymous != nil {
			t.Fatal(rows, err, source.anonymous)
		}
		for i, call := range calls {
			if call != 1 {
				t.Fatal("VALUES occurrence reevaluated/deduplicated", i, call)
			}
		}
		for i, row := range rows {
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
