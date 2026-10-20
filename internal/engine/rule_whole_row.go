package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) ruleWholeRow(side parser.Identifier, pos parser.Position) (expr.Expr, error) {
	d := b.Target.RuleNamespace.Relation
	fields := make([]expr.Expr, len(d.Columns))
	typ, _ := b.BindingCatalog().LookupType(d.RowType)
	var members []sqltype.CompositeMember
	if typ == nil || !typ.Concrete() {
		members = make([]sqltype.CompositeMember, len(d.Columns))
	}
	properties := expr.Properties{RowDependent: true}
	for i, column := range d.Columns {
		var err error
		fields[i], _, err = b.RuleColumn(parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{side, {Text: column.Name, Pos: pos}}}, Pos: pos})
		if err != nil {
			return nil, err
		}
		if members != nil {
			members[i] = sqltype.CompositeMember{Attribute: d.Attributes[i], Name: column.Name, Usage: column.Usage}
		}
		properties = expr.MergeProperties(properties, fields[i].Properties())
	}
	if typ == nil || !typ.Concrete() {
		var err error
		typ, err = sqltype.RecordShape(members)
		if err != nil {
			return nil, err
		}
	}
	ruleSide := uint32(1)
	if side.Text == "new" {
		ruleSide = 2
	}
	return plan.RowExpr{Info: b.Info(typ.Usage(), pos, properties), Fields: fields, RuleSide: ruleSide}, nil
}
