package parser

// IsTypeSingleton recognizes cast-derived scalar syntax, not type authority.
// The engine must bind each target and require an enum cast or JSON annotation.
// Missing types and modifiers consequently keep normal binder diagnostics.
func (s *Select) IsTypeSingleton() bool {
	if s.Set != nil || s.Distinct != nil || s.Parenthesized || s.Values != nil || s.From != nil || s.Where != nil || len(s.GroupBy) != 0 || s.Having != nil || s.Order != nil || s.Locking != nil || len(s.Targets) == 0 {
		return false
	}
	for _, target := range s.Targets {
		valid, cast := typeSingleton(target)
		if !valid || !cast {
			return false
		}
	}
	return true
}

func typeSingleton(raw Expr) (valid, cast bool) {
	switch e := raw.(type) {
	case LiteralExpr, ParameterExpr:
		return true, false
	case TypedLiteralExpr:
		return e.TypeName.Name == "json", e.TypeName.Name == "json"
	case CastExpr:
		valid, _ := typeSingleton(e.Child)
		return valid, true
	case CollateExpr:
		return typeSingleton(e.Child)
	case OperatorExpr:
		if len(e.Name.Parts) != 1 || len(e.Args) != 2 {
			return false, false
		}
		switch e.Name.Parts[0].Text {
		case "=", "<>", "!=", "<", "<=", ">", ">=":
		default:
			return false, false
		}
		left, a := typeSingleton(e.Args[0])
		right, b := typeSingleton(e.Args[1])
		return left && right, a || b
	case LogicalExpr:
		left, a := typeSingleton(e.Left)
		if e.Right == nil {
			return left, a
		}
		right, b := typeSingleton(e.Right)
		return left && right, a || b
	case PredicateExpr:
		left, a := typeSingleton(e.Left)
		if e.Right == nil {
			return left, a
		}
		right, b := typeSingleton(e.Right)
		return left && right, a || b
	default:
		return false, false
	}
}
