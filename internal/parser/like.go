package parser

func likeExpr(left, right, escape Expr, token lexeme, negated bool, pos Position) Expr {
	operator := "~~"
	if token.text == "ilike" {
		operator = "~~*"
	}
	if negated {
		operator = "!" + operator
	}
	if escape != nil {
		right = FunctionExpr{Name: ObjectName{Parts: []Identifier{{Text: "pg_catalog", Pos: pos}, {Text: "like_escape", Pos: pos}}}, Args: []Expr{right, escape}, Pos: pos}
	}
	return OperatorExpr{Name: ObjectName{Parts: []Identifier{{Text: operator, Pos: pos}}}, Args: []Expr{left, right}, Pos: pos}
}
