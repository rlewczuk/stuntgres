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
	// A real nearer range-table qualifier shadows the outer rule namespace.
	// Let the ordinary binder own member lookup and diagnostics, including a
	// missing column or a forbidden lateral reference; never fall back to OLD/
	// NEW merely because the local source lacks the requested member.
	if b.Query.Namespace != nil {
		for _, qualifier := range b.Query.Namespace.Qualifiers {
			if qualifier.Name == side {
				return nil, false, nil
			}
		}
	}
	for i := len(b.Query.Scopes) - 1; i >= 0; i-- {
		scope := &b.Query.Scopes[i]
		if !scope.allowed || scope.namespace == nil {
			continue
		}
		for _, qualifier := range scope.namespace.Qualifiers {
			if qualifier.Name == side {
				return nil, false, nil
			}
		}
	}
	ns := b.Target.RuleNamespace
	if ns.Trigger {
		if ns.TriggerStatement {
			return nil, true, expr.ErrorAt("42P17", "statement trigger's WHEN condition cannot reference column values", raw.Pos)
		}
		if side == "old" && ns.TriggerInsert {
			return nil, true, expr.ErrorAt("42P17", "INSERT trigger's WHEN condition cannot reference OLD values", raw.Pos)
		}
		if side == "new" && ns.TriggerDelete {
			return nil, true, expr.ErrorAt("42P17", "DELETE trigger's WHEN condition cannot reference NEW values", raw.Pos)
		}
		if side == "new" && !ns.TriggerAfter && len(raw.Name.Parts) > 1 {
			name := raw.Name.Parts[1].Text
			switch name {
			case "ctid", "xmin", "xmax", "cmin", "cmax", "tableoid":
				return nil, true, expr.ErrorAt("0A000", "BEFORE trigger's WHEN condition cannot reference NEW system columns", raw.Pos)
			}
			if slot, ok := ns.Relation.Index[name]; ok {
				if _, generated := ns.Relation.ColumnMeta.Generations[ns.Relation.Attributes[slot]]; generated {
					return nil, true, expr.ErrorAt("42P17", "BEFORE trigger's WHEN condition cannot reference NEW generated columns", raw.Pos)
				}
			}
		}
	}
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
