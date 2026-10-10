package parser

func similarExpr(left, pattern, escape Expr, negate bool, pos Position) Expr {
	args := []Expr{pattern}
	if escape != nil {
		args = append(args, escape)
	}
	normalized := FunctionExpr{Name: ObjectName{Parts: []Identifier{{Text: "pg_catalog", Pos: pos}, {Text: "similar_to_escape", Pos: pos}}}, Args: args, Syntax: SimilarEscapeFunctionSyntax, Pos: pos}
	op := "~"
	if negate {
		op = "!~"
	}
	return OperatorExpr{Name: ObjectName{Parts: []Identifier{{Text: op, Pos: pos}}}, Args: []Expr{left, normalized}, Pos: pos, SQLSimilar: true}
}
