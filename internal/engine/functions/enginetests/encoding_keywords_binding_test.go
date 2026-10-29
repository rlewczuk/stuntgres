package enginetests_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestEncodingKeywordsBinding(t *testing.T) {
	e := engine.New()
	for _, c := range []struct {
		input string
		formal, result sqltype.ID
		volatility expr.Volatility
	}{
		{"pg_char_to_encoding('UTF8')",sqltype.Name,sqltype.Int4,expr.Stable},
		{"pg_char_to_encoding(NULL)",sqltype.Name,sqltype.Int4,expr.Stable},
		{"pg_char_to_encoding($1)",sqltype.Name,sqltype.Int4,expr.Stable},
		{"pg_encoding_to_char(6)",sqltype.Int4,sqltype.Name,expr.Stable},
		{"pg_encoding_to_char(NULL)",sqltype.Int4,sqltype.Name,expr.Stable},
		{"pg_encoding_to_char($1)",sqltype.Int4,sqltype.Name,expr.Stable},
		{"pg_char_to_encoding(random()::text)",sqltype.Name,sqltype.Int4,expr.Volatile},
	} {
		t.Run(c.input,func(t *testing.T) {
			b := enginetest.ExpressionBinder(t,context.Background(),e,sqltype.InputEnvironment{},nil)
			b.Clause.AggregateClause = "SELECT"
			var raw parser.Expr
			if c.volatility == expr.Volatile {
				raw = datetimeMakeRaw("pg_char_to_encoding",parser.CastExpr{Child:datetimeMakeRaw("random"),TypeName:parser.TypeName{Name:"text"}})
			} else {
				raw = enginetest.Statement(t,"SELECT "+c.input).(*parser.Select).Targets[0]
			}
			node,err := b.Bind(raw,analyze.ExpressionPolicy{Context:analyze.ProjectionContext})
			if err != nil { t.Fatal(err) }
			call,ok := node.(functions.EncodingNameCall)
			if !ok || call.Arg.ResultUsage().Type.ID() != c.formal || call.ResultUsage().Type.ID() != c.result { t.Fatal("selected typed signature",node) }
			oid := uint32(1597)
			if call.ToID { oid = 1264 }
			if call.CallRoutineReference() == nil || call.CallRoutineReference().RoutineOID() != oid { t.Fatal("missing ordinary scalar routine identity") }
			if call.Properties().Volatility != c.volatility || call.IntrinsicVolatility() != expr.Stable || call.PlanningFold() != expr.FoldNever { t.Fatal("stable child policy",call.Properties()) }
			if b.Consumer.CatalogSnapshot || b.Prepared.RegclassConcat { t.Fatal("pure family demanded catalog runtime") }
			if _,err := call.Rebuild(call.Info,call.Children()); err != nil { t.Fatal(err) }
		})
	}
	b := enginetest.ExpressionBinder(t,context.Background(),e,sqltype.InputEnvironment{},nil)
	b.Clause.AggregateClause = "SELECT"
	raw := enginetest.Statement(t,"SELECT pg_get_keywords()").(*parser.Select).Targets[0]
	node,err := b.Bind(raw,analyze.ExpressionPolicy{Context:analyze.ProjectionContext})
	if err != nil { t.Fatal(err) }
	call,ok := node.(functions.Keywords)
	if !ok || call.Validate() != nil || call.ResultUsage().Type.ID() != 2249 || call.Properties().Volatility != expr.Stable { t.Fatal("fixed OUT binding",node) }
	if call.CallRoutineReference() == nil || call.CallRoutineReference().RoutineOID() != 1686 { t.Fatal("missing ordinary source routine identity") }
	if b.Consumer.CatalogSnapshot || b.Prepared.RegclassConcat { t.Fatal("keyword family demanded catalog runtime") }
}
