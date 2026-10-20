package analyze

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

func (b *Binder) RuleColumn(raw parser.ColumnExpr) (expr.Expr, bool, error) {
	if b.Target.RuleNamespace == nil || len(raw.Name.Parts) == 0 {
		return nil, false, nil
	}
	side := raw.Name.Parts[0].Text
	if side != "new" && side != "old" {
		return nil, false, nil
	}
	ns := b.Target.RuleNamespace
	if side == "old" && ns.Event == "INSERT" || side == "new" && ns.Event == "DELETE" || ns.Event == "SELECT" {
		return nil, true, sqlError("42P17", fmt.Sprintf("ON %s rule cannot use %s", ns.Event, strings.ToUpper(side)))
	}
	if len(raw.Name.Parts) == 1 {
		row, err := b.ruleWholeRow(raw.Name.Parts[0], raw.Pos)
		return row, true, err
	}
	if len(raw.Name.Parts) > 2 {
		first := raw
		first.Name.Parts = raw.Name.Parts[:2]
		base, _, err := b.RuleColumn(first)
		if err != nil {
			return nil, true, err
		}
		for _, field := range raw.Name.Parts[2:] {
			base, err = b.SelectCompositeField(base, field.Text, field.Pos)
			if err != nil {
				return nil, true, err
			}
		}
		return base, true, nil
	}
	name := raw.Name.Parts[1].Text
	slot, ok := ns.Relation.Index[name]
	if !ok {
		return nil, true, expr.ErrorAt("42703", fmt.Sprintf(`column %s.%s does not exist`, side, name), raw.Pos)
	}
	ref := ns.Ref
	ref.Address.SubID = ns.Relation.Attributes[slot]
	frame := uint32(1)
	if side == "new" {
		frame = 2
	}
	b.Dependencies = append(b.Dependencies, catalog.ExpressionDependency{Ref: ref, Revision: ns.Relation.Revision()})
	return plan.ColumnExpr{Info: b.Info(ns.Relation.Columns[slot].Usage, raw.Pos, expr.Properties{RowDependent: true}), Origin: ref, Scope: RulePseudoScope, Frame: frame, Slot: slot}, true, nil
}
