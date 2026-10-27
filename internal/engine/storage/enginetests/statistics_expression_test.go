package enginetests

import (
	"context"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func TestStatisticsExpressionCollectionSignedDimensions(t *testing.T) {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	integer := types[sqltype.Int4]
	selection, err := catalog.NewStatisticsSelection([]int32{2}, 2, "de")
	if err != nil {
		t.Fatal(err)
	}
	one, _ := sqltype.Integer(integer, 1)
	literal := plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: one}
	recipes := catalog.NewStatisticsExpressions([]catalog.StatisticsExpression{
		catalog.NewStatisticsExpression(literal, literal, nil, nil, nil, integer.Usage()),
		catalog.NewStatisticsExpression(literal, literal, nil, nil, nil, integer.Usage()),
	})
	definition := &catalog.Statistics{Selection: selection, Expressions: recipes}
	columns := []storage.StatisticsColumn{{Key: storage.StatisticsKey{Attribute: 2}, Usage: integer.Usage(), Slot: 0}, {Key: storage.StatisticsKey{Attribute: -1}, Usage: integer.Usage(), Slot: 1}, {Key: storage.StatisticsKey{Attribute: -2}, Usage: integer.Usage(), Slot: 2}}
	collector, err := storage.NewExtendedCollector(storage.ExtendedInput{Definition: definition, Columns: columns, Type: types[sqltype.PgNDistinct]})
	if err != nil {
		t.Fatal(err)
	}
	row := make([]sqltype.Value, 3)
	for _, n := range []int64{1, 1, 2, 2} {
		value, _ := sqltype.Integer(integer, n)
		for i := range row {
			row[i] = value
		}
		if err := collector.Add(context.Background(), row); err != nil {
			t.Fatal(err)
		}
	}
	for i := range row {
		row[i] = sqltype.Null(integer)
	}
	if err := collector.Add(context.Background(), row); err != nil {
		t.Fatal(err)
	}
	// Overwriting caller scratch cannot change retained groups or MCV data.
	for i := range row {
		row[i] = one
	}
	fact, err := collector.Finish(context.Background())
	if err != nil || fact.ExpressionLen() != 2 || fact.Recipes != recipes || len(fact.Items) != 4 {
		t.Fatal(fact, err)
	}
	expected := [][]int16{{2, -1}, {2, -2}, {-1, -2}, {2, -1, -2}}
	for i, item := range fact.Items {
		if !slices.Equal(item.Attributes, expected[i]) || item.Value != 3 {
			t.Fatal(i, item)
		}
	}
	for i := range fact.ExpressionLen() {
		if fact.Expression(i).Rows != 5 || fact.Expression(i).NullCount != 1 || fact.Expression(i).DistinctCount != 2 || len(fact.Expression(i).MCV) != 2 {
			t.Fatal(fact.Expression(i))
		}
	}
	if !fact.Value.ValidResult(types[sqltype.PgNDistinct].Usage()) {
		t.Fatal("joint payload lacks genuine type authority")
	}
}
