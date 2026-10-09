package parser

import "strings"

// Dedicated SQL syntax resolves pg_catalog independently of ordinary search_path.
func stringBuiltin(name string, pos Position, args []Expr) FunctionExpr {
	syntax := TrimStringSyntax
	if name == "normalize" {
		syntax = NormalizeStringSyntax
	}
	if name == "is_normalized" {
		syntax = NormalizedStringSyntax
	}
	return FunctionExpr{Name: ObjectName{Parts: []Identifier{{Text: "pg_catalog", Pos: pos}, {Text: name, Pos: pos}}}, Args: args, Pos: pos, StringSyntax: syntax}
}
func stringForm(form lexeme) Expr {
	switch form.text {
	case "nfc":
		form.text = "NFC"
	case "nfd":
		form.text = "NFD"
	case "nfkc":
		form.text = "NFKC"
	case "nfkd":
		form.text = "NFKD"
	}
	return LiteralExpr{Value: Value{Kind: StringValue, Text: form.text, Pos: form.pos}}
}
func normalizedExpr(child Expr, form lexeme, pos Position, negated bool) Expr {
	args := []Expr{child}
	if form.pos.Character != 0 {
		args = append(args, stringForm(form))
	}
	call := stringBuiltin("is_normalized", pos, args)
	if negated {
		return LogicalExpr{Op: LogicalNot, Left: call, Pos: pos}
	}
	return call
}

// ContainsStringExpression recognizes the selected family within the existing
// bounded scalar repertoire. It does not admit any additional SQL syntax.
func ContainsStringExpression(expr Expr) bool {
	valid, found := stringSingleton(expr)
	return valid && found
}

// String expressions form a bounded no-FROM exception. Their children use the
// existing scalar repertoire; this does not admit arbitrary singleton queries.
func stringSingleton(expr Expr) (valid, found bool) { return stringSingletonFamily(expr, false) }

// The datetime family admits scalar siblings only when a selected datetime
// call occurs somewhere in the bounded projection. Existing string admission
// retains its per-target family requirement.
func stringSingletonFamily(expr Expr, datetimeOnly bool) (valid, found bool) {
	switch e := expr.(type) {
	case LiteralExpr, TypedLiteralExpr, ParameterExpr, IdentityExpr, CurrentDateTimeExpr:
		return true, false
	case CastExpr:
		return stringSingletonFamily(e.Child, datetimeOnly)
	case CollateExpr:
		return stringSingletonFamily(e.Child, datetimeOnly)
	case RegclassExpr:
		return stringSingletonFamily(e.Child, datetimeOnly)
	case TimeZoneExpr:
		a, b := stringSingletonFamily(e.Value, datetimeOnly)
		if e.Local {
			return a, b
		}
		c, d := stringSingletonFamily(e.Zone, datetimeOnly)
		return a && c, b || d
	case FunctionExpr:
		if len(e.Name.Parts) == 0 {
			return false, false
		}
		name := e.Name.Parts[len(e.Name.Parts)-1].Text
		found = strings.EqualFold(name, "format") || name == "btrim" || name == "ltrim" || name == "rtrim" || name == "trim" || name == "normalize" || name == "is_normalized" || name == "lower" || name == "upper" || name == "casefold" || name == "initcap" || name == "overlay" || stringReplacementFunction(name) || datetimeFunctionName(name)
		if !found && !singletonArgument(e) {
			return false, false
		}
		if datetimeOnly {
			found = datetimeFunctionName(name)
		}
		for _, arg := range e.Args {
			ok, has := stringSingletonFamily(arg, datetimeOnly)
			if !ok {
				return false, false
			}
			found = found || has
		}
		return true, found
	case OperatorExpr:
		found = !datetimeOnly && len(e.Name.Parts) > 0 && (e.Name.Parts[len(e.Name.Parts)-1].Text == "||" || e.Name.Parts[len(e.Name.Parts)-1].Text == "^@")
		for _, arg := range e.Args {
			ok, has := stringSingletonFamily(arg, datetimeOnly)
			if !ok {
				return false, false
			}
			found = found || has
		}
		return true, found
	case LogicalExpr:
		a, b := stringSingletonFamily(e.Left, datetimeOnly)
		if e.Right == nil {
			return a, b
		}
		c, d := stringSingletonFamily(e.Right, datetimeOnly)
		return a && c, b || d
	case PredicateExpr:
		a, b := stringSingletonFamily(e.Left, datetimeOnly)
		if e.Right == nil {
			return a, b
		}
		c, d := stringSingletonFamily(e.Right, datetimeOnly)
		return a && c, b || d
	case ConditionalExpr:
		for _, arg := range e.Args {
			ok, has := stringSingletonFamily(arg, datetimeOnly)
			if !ok {
				return false, false
			}
			found = found || has
		}
		return true, found
	case CaseExpr:
		valid = true
		if e.Test != nil {
			ok, has := stringSingletonFamily(e.Test, datetimeOnly)
			valid = valid && ok
			found = found || has
		}
		for _, arm := range e.Arms {
			a, b := stringSingletonFamily(arm.Condition, datetimeOnly)
			c, d := stringSingletonFamily(arm.Result, datetimeOnly)
			valid = valid && a && c
			found = found || b || d
		}
		if e.Else != nil {
			ok, has := stringSingletonFamily(e.Else, datetimeOnly)
			valid = valid && ok
			found = found || has
		}
		return valid, found
	}
	return false, false
}
