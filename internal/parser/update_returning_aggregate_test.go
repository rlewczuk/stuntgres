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
	for _, q := range []string{"UPDATE t SET id=sum(old.id)", "UPDATE t SET id=1 WHERE sum(old.id)=1", "INSERT INTO t(id) VALUES(sum(t.id))", "INSERT INTO t(id) VALUES(sum(id))"} {
		statements, err := Parse(q)
		if err == nil || statements != nil {
			t.Fatal("aggregate child bypassed scalar qualification/input guard", q, statements, err)
		}
	}
}
