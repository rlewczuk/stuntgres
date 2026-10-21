package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
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
	return analyze.DefaultOrigin{Definition: definition, Consumer: physical.Relation, Quote: exec.QuoteOrigin{Owner: physical.Ref, Attribute: attribute, Definition: definition.OID}}
}
