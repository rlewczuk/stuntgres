package parser

import "strings"

func IdentityFunctionName(name string) bool {
	switch name {
	case "current_catalog", "current_database", "current_role", "current_user", "session_user", "user", "system_user", "current_schema":
		return true
	}
	// Quoted uppercase names must reach signature resolution without folding the
	// retained identifier. EqualFold does not allocate a normalized spelling.
	switch len(name) {
	case 4:
		return strings.EqualFold(name, "user")
	case 11:
		return strings.EqualFold(name, "system_user")
	case 12:
		return strings.EqualFold(name, "current_role") || strings.EqualFold(name, "current_user") || strings.EqualFold(name, "session_user")
	case 14:
		return strings.EqualFold(name, "current_schema")
	case 15:
		return strings.EqualFold(name, "current_catalog")
	case 16:
		return strings.EqualFold(name, "current_database")
	}
	return false
}

// identitySingleton admits session-value-derived scalar trees, with literals
// and parameters as siblings. It does not broaden general no-FROM SELECT.
func identitySingleton(raw Expr) (valid, found bool) {
	if raw == nil {
		return true, false
	}
	switch e := raw.(type) {
	case IdentityExpr, CurrentDateTimeExpr:
		return true, true
	case FunctionExpr:
		if len(e.Name.Parts) == 0 || !IdentityFunctionName(e.Name.Parts[len(e.Name.Parts)-1].Text) {
			return false, false
		}
		for _, arg := range e.Args {
			valid, _ := identitySingleton(arg)
			if !valid && !singletonArgument(arg) {
				return false, false
			}
		}
		return true, true
	case LiteralExpr, TypedLiteralExpr, ParameterExpr:
		return true, false
	case CollateExpr:
		return identitySingleton(e.Child)
	case CastExpr:
		return identitySingleton(e.Child)
	case TimeZoneExpr:
		return identitySingletonPair(e.Value, e.Zone)
	case OperatorExpr:
		return identitySingletonArgs(e.Args)
	case LogicalExpr:
		return identitySingletonPair(e.Left, e.Right)
	case PredicateExpr:
		return identitySingletonPair(e.Left, e.Right)
	case ConditionalExpr:
		return identitySingletonArgs(e.Args)
	case CaseExpr:
		valid, found = identitySingletonPair(e.Test, e.Else)
		if !valid {
			return false, false
		}
		for _, arm := range e.Arms {
			ok, identity := identitySingletonPair(arm.Condition, arm.Result)
			if !ok {
				return false, false
			}
			found = found || identity
		}
		return true, found
	}
	return false, false
}

func identitySingletonPair(a, b Expr) (bool, bool) {
	av, af := identitySingleton(a)
	if !av {
		return false, false
	}
	bv, bf := identitySingleton(b)
	return bv, af || bf
}

func identitySingletonArgs(args []Expr) (valid, found bool) {
	for _, arg := range args {
		ok, identity := identitySingleton(arg)
		if !ok {
			return false, false
		}
		found = found || identity
	}
	return true, found
}
