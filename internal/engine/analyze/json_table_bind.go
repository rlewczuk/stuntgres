package analyze

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

func (b *Binder) bindJSONTable(raw *parser.JSONTable, ordinal int) (plan.Node, QueryNamespace, error) {
	oldClause := b.Clause.AggregateClause
	b.Clause.AggregateClause = "FROM"
	defer func() { b.Clause.AggregateClause = oldClause }()
	oldForbidden := b.Clause.stringFromForbidden
	b.Clause.stringFromForbidden = false
	defer func() { b.Clause.stringFromForbidden = oldForbidden }()
	if b.Query.Namespace == nil {
		b.Query.Namespace = &QueryNamespace{}
		defer func() { b.Query.Namespace = nil }()
	}
	bound, err := functions.BindJSONTable(b, raw)
	if err != nil {
		return nil, QueryNamespace{}, err
	}
	table := bound.(functions.JSONTable)
	shape := bound.ResultUsage().Type
	schema := make([]catalog.Column, shape.CompositeLen())
	columns := make([]QueryColumn, len(schema))
	for i := range schema {
		member := shape.CompositeMember(i)
		schema[i] = catalog.Column{Name: member.Name, Usage: member.Usage}
		columns[i] = QueryColumn{Descriptor: schema[i], Slot: i, Leaf: -1, Physical: -1}
	}
	q := QueryQualifier{Name: "json_table", Original: "json_table", Columns: columns}
	if ordinal >= 0 {
		written := table.WrittenChildren()
		for i, child := range raw.Expressions() {
			path := ViewLiteralPath{Query: b.FrameID(), Clause: ViewFunctionLiteral, Source: ordinal, Target: i}
			at := 0
			if err := b.CaptureViewLiteralTree(child, written[i], path, &at); err != nil {
				return nil, QueryNamespace{}, err
			}
		}
	}
	captures := b.functionCaptures([]expr.Expr{bound})
	b.Query.Hidden = append(b.Query.Hidden, q)
	node := &plan.FunctionFromNode{QuerySchema: schema, Calls: []expr.Expr{bound}, Frame: b.FrameID(), Correlated: len(captures) > 0, Captures: captures, RegexpNamespace: true, ViewOrdinal: ordinal}
	return node, QueryNamespace{Columns: columns, Qualifiers: []QueryQualifier{q}, Frame: schema}, nil
}
