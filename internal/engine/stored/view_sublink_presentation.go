package stored

import "stuntgres/internal/parser"

// Every nested query establishes its own output-name visibility: sublink
// results are hidden, while derived sources and CTE bodies expose their names.
// Neither inherits an enclosing query's visibility or result descriptor.
func (p Presentation) nestedQueryPresentation(visible bool) Presentation {
	if p.Rule {
		p.Indent += 8
		p.HideLabels = !visible
		p.AnchorLabels = false
		p.Columns = nil
	}
	return p
}

// ruleSublinkSQL owns only the structural punctuation/quantifier policy. Its
// operands and nested query are rendered by the existing expression/query
// renderers; replay remains at their established caller boundaries.
func ruleSublinkSQL(kind parser.SubqueryKind, operator, left, query string, negate bool) string {
	var text string
	switch {
	case kind == parser.SubqueryScalar || kind == parser.SubqueryRow && left == "":
		text = "(" + query + ")"
	case kind == parser.SubqueryExists:
		text = "(EXISTS (" + query + "))"
	default:
		switch kind {
		case parser.SubqueryAll:
			operator += " ALL"
		case parser.SubqueryAny:
			if operator == "=" {
				operator = "IN"
			} else {
				operator += " ANY"
			}
		}
		text = "(" + left + " " + operator + " (" + query + "))"
	}
	if negate {
		text = "(NOT " + text + ")"
	}
	return text
}
