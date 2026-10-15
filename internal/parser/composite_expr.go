package parser

import "strings"

// ordinaryCall preserves existing aggregate/cast identities; other names are
// candidates for the ordinary function resolver's composite-field fallback.
func ordinaryCall(l sqlLexer, typ TypeName, args []Expr, callee lexeme) Expr {
	if ordinaryAggregate(typ.Name) {
		return AggregateExpr{Name: typ.Name, Schema: typ.Schema, Args: args, Pos: typ.Pos}
	}
	if ordinaryCastFunction(callee) && len(args) == 1 {
		excluded := false
		switch child := args[0].(type) {
		case PredicateExpr, LogicalExpr, InListExpr, ArrayComparisonExpr:
			excluded = true
		case OperatorExpr:
			if len(child.Name.Parts) == 1 {
				switch child.Name.Parts[0].Text {
				case "=", "<", ">", "<=", ">=", "<>":
					excluded = true
				}
			}
		}
		if excluded {
			l.(*lexer).failAt("0A000", "functions are unsupported", typ.Pos.Offset)
			return nil
		}
		return CastExpr{Child: args[0], TypeName: typ, Syntax: FunctionCast, Callee: callee.text, Pos: typ.Pos}
	}
	parts := []Identifier{{Text: typ.Name, Pos: typ.Pos}}
	if typ.Schema != nil {
		parts = append([]Identifier{*typ.Schema}, parts...)
	}
	return FunctionExpr{Name: ObjectName{Parts: parts}, Args: args, Pos: typ.Pos, CompositeCandidate: !ordinarySignatureName(typ.Name)}
}

func modifiedOrdinaryCall(typ TypeName, args []Expr, star, distinct, all, variadic bool, order []OrderKey) Expr {
	if ordinaryAggregate(typ.Name) {
		return AggregateExpr{Name: typ.Name, Schema: typ.Schema, Args: args, Star: star, Distinct: distinct, Variadic: variadic, Order: order, Pos: typ.Pos}
	}
	parts := []Identifier{{Text: typ.Name, Pos: typ.Pos}}
	if typ.Schema != nil {
		parts = append([]Identifier{*typ.Schema}, parts...)
	}
	return FunctionExpr{Name: ObjectName{Parts: parts}, Args: args, Pos: typ.Pos, Star: star, Distinct: distinct, All: all, Variadic: variadic, Order: order, CompositeCandidate: !ordinarySignatureName(typ.Name)}
}

// Only the unquoted release JSON_OBJECT legacy production rewrites its name.
// Quoted and schema-qualified calls retain ordinary resolution identities.
func selectedScalarCall(l sqlLexer, callee lexeme, call FunctionExpr) (FunctionExpr, bool) {
	call.Name = ObjectName{Parts: []Identifier{{Text: callee.text, Pos: callee.pos}}}
	call.Pos, call.ResolutionOnly = callee.pos, callee.resolutionOnly
	if callee.keyword != nil && callee.text == "json_object" {
		call.Name.Parts = []Identifier{{Text: "pg_catalog", Pos: callee.pos}, {Text: "json_object", Pos: callee.pos}}
	}
	return call, true
}

// Selected names and aggregate case variants retain missing-signature diagnostics
// rather than the generic scalar-function capability boundary. This includes
// jsonb_objectagg, which is deliberately not a registered function or alias.
// Eligible one-argument composite-field fallback still uses exact-name resolution.
func ordinarySignatureName(name string) bool {
	return strings.EqualFold(name, "left") || strings.EqualFold(name, "right") ||
		strings.EqualFold(name, "substring") || strings.EqualFold(name, "substr") ||
		strings.EqualFold(name, "sum") || strings.EqualFold(name, "json_agg") || strings.EqualFold(name, "jsonb_agg") ||
		strings.EqualFold(name, "json_agg_strict") || strings.EqualFold(name, "jsonb_agg_strict") ||
		strings.EqualFold(name, "json_object_agg") || strings.EqualFold(name, "jsonb_object_agg") ||
		strings.EqualFold(name, "json_object_agg_strict") || strings.EqualFold(name, "jsonb_object_agg_strict") ||
		strings.EqualFold(name, "json_object_agg_unique") || strings.EqualFold(name, "jsonb_object_agg_unique") ||
		strings.EqualFold(name, "json_object_agg_unique_strict") || strings.EqualFold(name, "jsonb_object_agg_unique_strict") ||
		strings.EqualFold(name, "jsonb_objectagg") ||
		strings.EqualFold(name, "rank") || strings.EqualFold(name, "dense_rank") ||
		strings.EqualFold(name, "percent_rank") || strings.EqualFold(name, "cume_dist") ||
		strings.EqualFold(name, "percentile_cont") || strings.EqualFold(name, "percentile_disc") || strings.EqualFold(name, "mode")
}

// WITHIN owns the aggregate input order, rather than an ordinary in-call order.
func withinCall(l sqlLexer, expr Expr, token lexeme, order []OrderKey) (Expr, bool) {
	var distinct, variadic bool
	var prior []OrderKey
	switch call := expr.(type) {
	case AggregateExpr:
		distinct, variadic, prior = call.Distinct, call.Variadic, call.Order
	case FunctionExpr:
		distinct, variadic, prior = call.Distinct, call.Variadic, call.Order
	default:
		l.(*lexer).syntax(token)
		return nil, false
	}
	message := ""
	if len(prior) > 0 {
		message = "cannot use multiple ORDER BY clauses with WITHIN GROUP"
	} else if distinct {
		message = "cannot use DISTINCT with WITHIN GROUP"
	} else if variadic {
		message = "cannot use VARIADIC with WITHIN GROUP"
	}
	if message != "" {
		l.(*lexer).failAt("42601", message, token.pos.Offset)
		return nil, false
	}
	switch call := expr.(type) {
	case AggregateExpr:
		call.Within, call.WithinPos, call.Order = true, token.pos, order
		return call, true
	case FunctionExpr:
		call.Within, call.WithinPos, call.Order = true, token.pos, order
		return call, true
	}
	panic("unreachable WITHIN call")
}
