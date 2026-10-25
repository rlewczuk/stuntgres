package engine

import (
	"stuntgres/internal/parser"
)

func alterGenerationName(name string) parser.ObjectName {
	return parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}
}

func alterGenerationColumn(name string) parser.Expr {
	return parser.ColumnExpr{Name: alterGenerationName(name)}
}

func alterGenerationOperator(name string, args ...parser.Expr) parser.Expr {
	return parser.OperatorExpr{Name: alterGenerationName(name), Args: args}
}
