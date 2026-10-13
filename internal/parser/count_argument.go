package parser

// Shared call openers admit general value expressions only for count. Function
// casts keep their previous scalar (rather than predicate) argument boundary.
func scalarCastArgument(expr Expr) bool {
	switch e := expr.(type) {
	case PredicateExpr, LogicalExpr, InListExpr, ArrayComparisonExpr:
		return false
	case OperatorExpr:
		if len(e.Name.Parts) == 1 {
			switch e.Name.Parts[0].Text {
			case "=", "<>", "!=", "<", ">", "<=", ">=":
				return false
			}
		}
	}
	return true
}
