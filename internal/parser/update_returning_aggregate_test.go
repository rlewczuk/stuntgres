package parser

import "testing"

func TestUpdateReturningAggregateSyntax(t *testing.T) {
	statements, err := Parse("UPDATE t SET id=1 RETURNING pg_catalog.sum(COALESCE(old.id,new.id))")
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	update := statements[0].(*Update)
	aggregate, ok := update.Returning.Targets[0].(AggregateExpr)
	if !ok || aggregate.Name != "sum" || aggregate.Schema == nil || aggregate.Schema.Text != "pg_catalog" || aggregate.Pos.Character != 29 {
		t.Fatal(aggregate)
	}
	if len(aggregate.Args) != 1 {
		t.Fatal("aggregate argument discarded", aggregate.Args)
	}
	if _, ok := aggregate.Args[0].(ConditionalExpr); !ok {
		t.Fatal("aggregate argument discarded", aggregate.Args[0])
	}
	for _, q := range []string{"INSERT INTO t(id) VALUES(sum(t.id))"} {
		statements, err := Parse(q)
		if err == nil || statements != nil {
			t.Fatal("aggregate child bypassed scalar qualification/input guard", q, statements, err)
		}
	}
	for _, q := range []string{"UPDATE t SET id=sum(old.id)", "UPDATE t SET id=1 WHERE sum(old.id)=1"} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal("UPDATE aggregate must reach semantic analysis", q, statements, err)
		}
		update := statements[0].(*Update)
		var expression Expr = update.Assignments[0].Value
		if update.Where != nil {
			comparison, ok := update.Where.(OperatorExpr)
			if !ok || len(comparison.Args) != 2 {
				t.Fatal("WHERE aggregate comparison AST", update.Where)
			}
			expression = comparison.Args[0]
		}
		aggregate, ok := expression.(AggregateExpr)
		if !ok || aggregate.Name != "sum" || len(aggregate.Args) != 1 {
			t.Fatal("UPDATE aggregate AST", expression)
		}
		column, ok := aggregate.Args[0].(ColumnExpr)
		if !ok || len(column.Name.Parts) != 2 || column.Name.Parts[0].Text != "old" || column.Name.Parts[1].Text != "id" {
			t.Fatal("UPDATE aggregate qualification AST", aggregate.Args)
		}
	}
}
