package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func TestAnalyzeForeignZeroDistinctEstimate(t *testing.T) {
	e, s, wrapper := foreignAnalyzeOwnerFixture(t)
	wrapper.sample = catalog.ForeignSample{Rows: []catalog.ForeignRow{{int64(1), int64(10)}, {int64(1), int64(20)}, {int64(2), int64(30)}, {int64(3), int64(40)}}, TotalRows: 40, TotalPages: 2}
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "foreign_analyze_owner")
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
	value, err := sqltype.Integer(integer, 2)
	if err != nil {
		t.Fatal(err)
	}
	column := plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage()}, Scope: 1, Frame: 1, Slot: 0, QueryOccurrence: 1}
	binding, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, integer, integer, boolean, integer.Usage().Collation, false)
	if err != nil {
		t.Fatal(err)
	}
	predicate := plan.ComparisonExpr{Info: expr.Info{Usage: boolean.Usage()}, Left: column, Right: plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: value}, Operator: "=", Predicate: binding}
	for _, test := range []struct {
		name, action  string
		encoded, rows float64
	}{
		{"normal", "", -.125, 5},
		{"explicit zero", "ALTER FOREIGN TABLE foreign_analyze_owner ALTER a SET (n_distinct=0)", -.125, 5},
		{"nonzero", "ALTER FOREIGN TABLE foreign_analyze_owner ALTER a SET (n_distinct=2)", 2, 20},
		{"reset", "ALTER FOREIGN TABLE foreign_analyze_owner ALTER a RESET (n_distinct)", -.125, 5},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.action != "" {
				enginetest.SessionSQL(t, s, test.action)
			}
			enginetest.SessionSQL(t, s, "ANALYZE foreign_analyze_owner")
			relation := e.CatalogRead().MustRelation(ref.Address)
			metadata := e.Storage.RelationMetadata[ref.Address]
			fact := metadata.Statistics.Columns[storage.StatisticsKey{Attribute: relation.Attributes[0]}]
			if metadata.ForeignAnalyze.SampleRows != 4 || metadata.ForeignAnalyze.TotalRows != 40 || fact.Rows != 4 || fact.DistinctCount != 3 || fact.NullCount != 0 || len(fact.MCV) != 1 || fact.MCV[0].Count != 2 || fact.DistinctOverride != test.encoded {
				t.Fatal("option changed literal counts or discarded normal sample estimate", metadata.ForeignAnalyze, fact)
			}
			selectivity, known, err := plan.EstimateSelectivity(context.Background(), predicate, []plan.ColumnStatisticInput{{Column: column, Statistics: fact, SourceRows: 40}})
			if err != nil || !known || selectivity*40 != test.rows {
				t.Fatal("sample singleton estimate", selectivity, known, err, test.rows)
			}
		})
	}
}
