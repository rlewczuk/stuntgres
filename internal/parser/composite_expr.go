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
	return FunctionExpr{Name: ObjectName{Parts: parts}, Args: args, Pos: typ.Pos, CompositeCandidate: !quotedJSONArrayAggregateName(typ.Name)}
}

func modifiedOrdinaryCall(typ TypeName, args []Expr, star, distinct, all, variadic bool, order []OrderKey) Expr {
	if ordinaryAggregate(typ.Name) {
		return AggregateExpr{Name: typ.Name, Schema: typ.Schema, Args: args, Star: star, Distinct: distinct, Order: order, Pos: typ.Pos}
	}
	parts := []Identifier{{Text: typ.Name, Pos: typ.Pos}}
	if typ.Schema != nil {
		parts = append([]Identifier{*typ.Schema}, parts...)
	}
	return FunctionExpr{Name: ObjectName{Parts: parts}, Args: args, Pos: typ.Pos, Star: star, Distinct: distinct, All: all, Variadic: variadic, Order: order, CompositeCandidate: !quotedJSONArrayAggregateName(typ.Name)}
}

// Only the unquoted release JSON_OBJECT legacy production rewrites its name.
// Quoted and schema-qualified calls retain ordinary resolution identities.
func selectedScalarCall(l sqlLexer, callee lexeme, call FunctionExpr) (FunctionExpr, bool) {
	call.Name = ObjectName{Parts: []Identifier{{Text: callee.text, Pos: callee.pos}}}
	call.Pos, call.ResolutionOnly = callee.pos, callee.resolutionOnly
	if callee.keyword != nil && callee.text == "json_object" {
		if len(call.Args) == 0 || call.Variadic {
			l.(*lexer).failAt("0A000", "functions are unsupported", callee.pos.Offset)
			return FunctionExpr{}, false
		}
		call.Name.Parts = []Identifier{{Text: "pg_catalog", Pos: callee.pos}, {Text: "json_object", Pos: callee.pos}}
	}
	return call, true
}

// Known aggregate case variants retain missing-signature diagnostics rather than
// the generic scalar-function capability boundary. They still use ordinary
// exact-name resolution and eligible one-argument composite-field fallback.
func quotedJSONArrayAggregateName(name string) bool {
	return strings.EqualFold(name,"json_agg") || strings.EqualFold(name,"jsonb_agg") ||
		strings.EqualFold(name,"json_agg_strict") || strings.EqualFold(name,"jsonb_agg_strict")
}
