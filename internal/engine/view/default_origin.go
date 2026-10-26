package view

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func BindingDefaultOrigin(b *analyze.Binding, slot int) analyze.DefaultOrigin {
	if slot < len(b.Insert.DefaultOrigins) {
		return b.Insert.DefaultOrigins[slot]
	}
	physical := PhysicalBinding(*b)
	attribute := physical.Relation.Attributes[slot]
	definition := physical.Relation.ColumnMeta.Defaults[attribute]
	if definition.Expression == nil {
		return analyze.DefaultOrigin{}
	}
	return analyze.DefaultOrigin{Definition: definition, Consumer: physical.Relation, Fold: exec.FoldOrigin{Owner: physical.Ref, Attribute: attribute, Definition: definition.OID}}
}

// EvalBindingDefault evaluates the default of binding slot.
func EvalBindingDefault(b *analyze.Binding, c *exec.Context, slot int, node expr.Expr) (sqltype.Value, error) {
	return analyze.EvalDefault(c, node, BindingDefaultOrigin(b, slot))
}
