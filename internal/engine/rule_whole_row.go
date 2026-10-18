package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) ruleWholeRow(side parser.Identifier, pos parser.Position) (expr.Expr, error) {
	d := b.ruleNamespace.relation
	fields := make([]expr.Expr, len(d.columns))
	typ, _ := b.bindingCatalog().LookupType(d.rowType)
	var members []sqltype.CompositeMember
	if typ == nil || !typ.Concrete() {
		members = make([]sqltype.CompositeMember, len(d.columns))
	}
	properties := expr.Properties{RowDependent: true}
	for i, column := range d.columns {
		var err error
		fields[i], _, err = b.ruleColumn(parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{side, {Text: column.Name, Pos: pos}}}, Pos: pos})
		if err != nil {
			return nil, err
		}
		if members != nil {
			members[i] = sqltype.CompositeMember{Attribute: d.attributes[i], Name: column.Name, Usage: column.Usage}
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
	return rowExpression{Info: b.info(typ.Usage(), pos, properties), fields: fields, ruleSide: ruleSide}, nil
}
