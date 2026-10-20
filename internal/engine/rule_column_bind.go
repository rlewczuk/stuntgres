package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

func (b *expressionBinder) ruleColumn(raw parser.ColumnExpr) (expr.Expr, bool, error) {
	if b.target.ruleNamespace == nil || len(raw.Name.Parts) == 0 {
		return nil, false, nil
	}
	side := raw.Name.Parts[0].Text
	if side != "new" && side != "old" {
		return nil, false, nil
	}
	ns := b.target.ruleNamespace
	if side == "old" && ns.event == "INSERT" || side == "new" && ns.event == "DELETE" || ns.event == "SELECT" {
		return nil, true, sqlError("42P17", fmt.Sprintf("ON %s rule cannot use %s", ns.event, strings.ToUpper(side)))
	}
	if len(raw.Name.Parts) == 1 {
		row, err := b.ruleWholeRow(raw.Name.Parts[0], raw.Pos)
		return row, true, err
	}
	if len(raw.Name.Parts) > 2 {
		first := raw
		first.Name.Parts = raw.Name.Parts[:2]
		base, _, err := b.ruleColumn(first)
		if err != nil {
			return nil, true, err
		}
		for _, field := range raw.Name.Parts[2:] {
			base, err = b.selectCompositeField(base, field.Text, field.Pos)
			if err != nil {
				return nil, true, err
			}
		}
		return base, true, nil
	}
	name := raw.Name.Parts[1].Text
	slot, ok := ns.relation.Index[name]
	if !ok {
		return nil, true, expr.ErrorAt("42703", fmt.Sprintf(`column %s.%s does not exist`, side, name), raw.Pos)
	}
	ref := ns.ref
	ref.Address.SubID = ns.relation.Attributes[slot]
	frame := uint32(1)
	if side == "new" {
		frame = 2
	}
	b.dependencies = append(b.dependencies, catalog.ExpressionDependency{Ref: ref, Revision: ns.relation.Revision()})
	return plan.ColumnExpr{Info: b.info(ns.relation.Columns[slot].Usage, raw.Pos, expr.Properties{RowDependent: true}), Origin: ref, Scope: rulePseudoScope, Frame: frame, Slot: slot}, true, nil
}
