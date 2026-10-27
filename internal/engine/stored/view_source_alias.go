package stored

import (
	"strings"

	"stuntgres/internal/parser"
)

// Logical source aliases preserve actual current source names and only explicit
// column aliases, for public inquiry and policy replay. Ordinary view dump's
// positional bridge aliases remain owned by aliasFrom.
func (v *QueryRenderer) sourceAlias(from viewRenderFrom, name string, columns []parser.Identifier, declare bool) viewRenderFrom {
	identifier := v.Presentation.identifier
	qualifier := identifier(name)
	from.slots = make(map[int]string, len(from.columns))
	for i := range from.columns {
		if i < len(columns) {
			from.columns[i].name = columns[i].Text
		}
		column := from.columns[i]
		if column.slot >= 0 {
			from.slots[column.slot] = qualifier + "." + identifier(column.name)
		}
	}
	text := from.relation
	if text == "" {
		text = "(" + from.text + ")"
	}
	from.text = text
	if declare {
		from.text += " " + qualifier
	}
	if v.logicalSourceNames() {
		from.wholeSQL = qualifier
		if v.policyReplaySources {
			from.alias = name
		}
	}
	if from.rowSource {
		from.rowTargetSQL = qualifier
	}
	if len(columns) != 0 {
		count := len(columns)
		if v.Presentation.Rule {
			count = len(from.columns)
		}
		labels := make([]string, count)
		for i := range labels {
			var name string
			if v.Presentation.Rule {
				name = from.columns[i].name
			} else {
				name = columns[i].Text
			}
			labels[i] = identifier(name)
		}
		from.text += "(" + strings.Join(labels, ", ") + ")"
	}
	from.relation = ""
	v.Slots[from.root] = from.slots
	v.setSourceQualifier(from.root, qualifier)
	return from
}
