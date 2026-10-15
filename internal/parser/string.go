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

// This tree detector is only assignment-family classification. Relationless
// query admission is structural and independent of selected function families.
func stringSingleton(expr Expr) (valid, found bool) { return stringSingletonFamily(expr, false) }

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
	case FieldExpr:
		return stringSingletonFamily(e.Base, datetimeOnly)
	case RowExpr:
		valid, found := true, false
		for _, element := range e.Fields {
			a, b := stringSingletonFamily(element, datetimeOnly)
			valid, found = valid && a, found || b
		}
		return valid, found
	case ArrayExpr:
		valid, found := true, false
		for _, element := range e.Elements {
			a, b := stringSingletonFamily(element, datetimeOnly)
			valid, found = valid && a, found || b
		}
		return valid, found
	case SubscriptExpr:
		var storage [13]Expr
		valid, found := true, false
		for _, child := range e.Children(&storage) {
			a, b := stringSingletonFamily(child, datetimeOnly)
			valid, found = valid && a, found || b
		}
		return valid, found
	case TimeZoneExpr:
		a, b := stringSingletonFamily(e.Value, datetimeOnly)
		if e.Local {
			return a, b
		}
		c, d := stringSingletonFamily(e.Zone, datetimeOnly)
		return a && c, b || d
	case SQLJSONExpr:
		valid, found := true, false
		for _, arg := range e.Args {
			a, b := stringSingletonFamily(arg, datetimeOnly)
			valid, found = valid && a, found || b
		}
		return valid, found
	case JSONPredicateExpr:
		return stringSingletonFamily(e.Child, datetimeOnly)
	case FunctionExpr:
		if len(e.Name.Parts) == 0 {
			return false, false
		}
		name := e.Name.Parts[len(e.Name.Parts)-1].Text
		found = strings.EqualFold(name, "format") || name == "btrim" || name == "ltrim" || name == "rtrim" || name == "trim" || name == "normalize" || name == "is_normalized" || name == "lower" || name == "upper" || name == "casefold" || name == "initcap" || name == "overlay" || stringReplacementFunction(name) || stringPaddingFunction(name) || datetimeFunctionName(name)
		found = found || stringSplitFunction(name)
		found = found || e.Syntax == SimilarEscapeFunctionSyntax
		found = found || !datetimeOnly && (RangeInspectFunctionName(name) || UUIDExtractFunctionName(name))
		found = found || !datetimeOnly && (RegexpFunctionName(name) || strings.EqualFold(name, "json_object_keys") || strings.EqualFold(name, "json_each") || strings.EqualFold(name, "json_each_text") || strings.EqualFold(name, "json_array_elements") || strings.EqualFold(name, "json_array_elements_text") || strings.EqualFold(name, "generate_series") || strings.EqualFold(name, "generate_subscripts") || strings.EqualFold(name, "unnest"))
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
		found = !datetimeOnly && (e.SQLSimilar || len(e.Name.Parts) > 0 && (e.Name.Parts[len(e.Name.Parts)-1].Text == "||" || e.Name.Parts[len(e.Name.Parts)-1].Text == "^@"))
		for _, arg := range e.Args {
			ok, has := stringSingletonFamily(arg, datetimeOnly)
			if !ok {
				return false, false
			}
			found = found || has
		}
		return true, found
	case InListExpr:
		valid, found := stringSingletonFamily(e.Left, datetimeOnly)
		for _, item := range e.Items {
			ok, has := stringSingletonFamily(item, datetimeOnly)
			valid, found = valid && ok, found || has
		}
		return valid, found
	case ArrayComparisonExpr:
		a, b := stringSingletonFamily(e.Left, datetimeOnly)
		c, d := stringSingletonFamily(e.Right, datetimeOnly)
		return a && c, b || d
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
