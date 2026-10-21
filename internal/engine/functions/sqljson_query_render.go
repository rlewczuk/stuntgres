package functions

import (
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

// SQLJSONQueryPieces uses the same written-order child vector as capture and
// reconstruction. Identifier/type quoting is owned by the engine renderers.
func SQLJSONQueryPieces(raw parser.SQLJSONQueryExpr) []expr.Piece {
	pieces := []expr.Piece{expr.TextPiece(strings.ToUpper(raw.Kind) + "("), expr.ArgPiece(0), expr.TextPiece(JSONFormatSQL(raw.Input) + ", "), expr.ArgPiece(1)}
	for i, binding := range raw.Passing {
		separator := ", "
		if i == 0 {
			separator = " PASSING "
		}
		pieces = append(pieces, expr.TextPiece(separator), expr.ArgPiece(i+2), expr.TextPiece(" AS "), expr.Piece{Identifier: true, Text: binding.Name.Text})
	}
	if raw.Returning != nil {
		pieces = append(pieces, expr.Piece{Returning: true, Text: " RETURNING ", Suffix: JSONFormatSQL(raw.Output)})
	}
	switch raw.Wrapper {
	case "without":
		pieces = append(pieces, expr.TextPiece(" WITHOUT WRAPPER"))
	case "conditional":
		pieces = append(pieces, expr.TextPiece(" WITH CONDITIONAL WRAPPER"))
	case "unconditional":
		pieces = append(pieces, expr.TextPiece(" WITH UNCONDITIONAL WRAPPER"))
	}
	if raw.Quotes != "" {
		pieces = append(pieces, expr.TextPiece(" "+strings.ToUpper(raw.Quotes)+" QUOTES"))
	}
	child := 2 + len(raw.Passing)
	for i, behavior := range []parser.SQLJSONBehavior{raw.Empty, raw.Error} {
		if behavior.Kind == "" {
			continue
		}
		pieces = append(pieces, expr.TextPiece(" "+strings.ToUpper(behavior.Kind)))
		if behavior.Default != nil {
			pieces = append(pieces, expr.TextPiece(" "), expr.ArgPiece(child))
			child++
		}
		branch := "ERROR"
		if i == 0 {
			branch = "EMPTY"
		}
		pieces = append(pieces, expr.TextPiece(" ON "+branch))
	}
	return append(pieces, expr.TextPiece(")"))
}
