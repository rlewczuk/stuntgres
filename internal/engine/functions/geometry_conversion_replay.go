package functions

import "stuntgres/internal/parser"

// CircleConstructorReplay admits only the qualified literal codec emitted by
// sqltype's negative-circle SQLLiteral. It is not general function admission.
// The normal binder still owns literal input, overload and datum validation.
func CircleConstructorReplay(r parser.FunctionExpr) bool {
	// CompositeCandidate is the parser's ordinary-name fallback marker (also
	// set on circle), not a decoration or selected provider authority.
	if len(r.Name.Parts) != 2 || r.Name.Parts[0].Text != "pg_catalog" || r.Name.Parts[1].Text != "circle" || len(r.Args) != 2 || len(r.ArgNames) != 0 || r.Variadic || r.Star || r.Distinct || r.All || len(r.Order) != 0 || r.Filter != nil || r.Within || r.Window != nil || len(r.ColumnDefinitions) != 0 || r.ResolutionOnly || r.StoredSQLRoutine || r.Syntax != 0 || r.StringSyntax != 0 {
		return false
	}
	for i, name := range [2]string{"point", "float8"} {
		cast, ok := r.Args[i].(parser.CastExpr)
		if !ok {
			return false
		}
		t := cast.TypeName
		if t.Schema == nil || t.Schema.Text != "pg_catalog" || t.Name != name || len(t.Modifiers) != 0 || len(t.ArrayBounds) != 0 || t.Setof {
			return false
		}
		literal, ok := cast.Child.(parser.LiteralExpr)
		if !ok || literal.Value.Kind != parser.StringValue {
			return false
		}
	}
	return true
}
