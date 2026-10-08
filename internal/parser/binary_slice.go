package parser

func binarySQLCall(name string, pos Position, args []Expr) FunctionExpr {
	return FunctionExpr{Name: ObjectName{Parts: []Identifier{{Text: "pg_catalog", Pos: pos}, {Text: name, Pos: pos}}}, Args: args, Pos: pos, Syntax: SQLFunctionSyntax}
}

func binarySubstringStart() Expr {
	return LiteralExpr{Value: Value{Kind: IntegerValue, Integer: 1, Pos: Position{Offset: -1, Character: -1}}}
}

func binarySubstringCount(child Expr) Expr {
	pos := Position{Offset: -1, Character: -1}
	return CastExpr{Child: child, TypeName: TypeName{Name: "int4", Schema: &Identifier{Text: "pg_catalog", Pos: pos}, Pos: pos}, Syntax: PostfixCast, Pos: pos}
}
