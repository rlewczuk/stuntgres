package enginetest

import (
	"stuntgres/internal/engine/core"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func RuleUnitRef(e *core.Engine, d *catalog.Relation) ids.ObjectRef {
	return e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Address())
}

func RuleUnitInt(d *catalog.Relation, n int64) expr.Expr {
	value, err := sqltype.Integer(d.Columns[0].Usage.Type, n)
	if err != nil {
		panic(err)
	}
	return plan.LiteralExpr{Info: expr.Info{Usage: d.Columns[0].Usage, Pos: parser.Position{Offset: -1}}, Value: value}
}

func RuleUnitInsert(e *core.Engine, d *catalog.Relation, id, v int64) stored.RuleAction {
	binding := analyze.Binding{Relation: d, Ref: RuleUnitRef(e, d), Targets: []int{0, 1}, Expressions: [][]expr.Expr{{RuleUnitInt(d, id), RuleUnitInt(d, v)}}}
	presentation := stored.NewView(stored.ViewParts{Query: &plan.Query{Root: &plan.ProjectNode{QuerySchema: plan.QuerySchema(d.Columns), Child: &plan.SingletonNode{}, Expressions: binding.Expressions[0]}}})
	return stored.NewRuleAction(stored.RuleActionParts{Syntax: &parser.Insert{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: d.Name()}}}}, Binding: binding, Presentation: presentation})
}
