package stored

import (
	"strings"
)

// line emits a structural continuation, relative to the current query/action.
// Initial command spacing remains owned by the caller, as in ruleutils.c.
func (p Presentation) line(offset int) string {
	return "\n" + strings.Repeat(" ", p.Indent+offset)
}

func (v *QueryRenderer) ruleDerivedSource(from viewRenderFrom, name ruleRangeName) viewRenderFrom {
	relation := "(" + from.text + ")"
	from.relation = relation
	from = v.sourceAlias(from, name.name, name.columns, true)
	from.relation = relation
	return from
}

func (v *QueryRenderer) ruleCTESource(from viewRenderFrom, cte string, name ruleRangeName) viewRenderFrom {
	relation := from.relation
	from = v.sourceAlias(from, name.name, name.columns, name.explicit || name.name != cte || len(name.columns) != 0)
	from.relation = relation
	return from
}
