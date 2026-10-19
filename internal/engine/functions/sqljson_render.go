package functions

import (
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

// SQLJSONPieces describes constructor syntax over operands in written order.
// The query form uses one already-rendered query operand: query analysis,
// capture and execution remain in the engine. RETURNING is a named slot whose
// type declaration comes only from the owning engine conversion or raw capture.
func SQLJSONPieces(raw parser.SQLJSONExpr, count int) []expr.Piece {
	pieces := []expr.Piece{expr.TextPiece(strings.ToUpper(raw.Kind) + "(")}
	for i := range count {
		if i > 0 {
			if raw.Kind == "json_object" && raw.Query == nil && i%2 == 1 {
				pieces = append(pieces, expr.TextPiece(") VALUE "))
			} else {
				pieces = append(pieces, expr.TextPiece(", "))
			}
		}
		if raw.Kind == "json_object" && raw.Query == nil && i%2 == 0 {
			pieces = append(pieces, expr.TextPiece("("))
		}
		pieces = append(pieces, expr.ArgPiece(i))
		if i < len(raw.Formats) {
			if format := JSONFormatSQL(raw.Formats[i]); format != "" {
				pieces = append(pieces, expr.TextPiece(format))
			}
		}
	}
	objectMembers := raw.Kind == "json_object" && count != 0
	hasBody := count != 0
	if raw.Query == nil && (raw.Kind == "json_array" || objectMembers) {
		clause := " NULL ON NULL"
		if raw.Absent {
			clause = " ABSENT ON NULL"
		}
		pieces = append(pieces, expr.TextPiece(clause))
		hasBody = true
	}
	if raw.Unique && (raw.Kind != "json_object" || objectMembers) {
		pieces = append(pieces, expr.TextPiece(" WITH UNIQUE KEYS"))
		hasBody = true
	}
	if raw.Kind == "json_array" || raw.Kind == "json_object" || raw.Kind == "json_serialize" {
		prefix := "RETURNING "
		if hasBody {
			prefix = " " + prefix
		}
		pieces = append(pieces, expr.Piece{Returning: true, Text: prefix, Suffix: JSONFormatSQL(raw.Output)})
	}
	return append(pieces, expr.TextPiece(")"))
}

// JSONFormatSQL is the shared SQL/JSON input/output format suffix, including
// its leading space. It carries syntax only, never a conversion plan.
func JSONFormatSQL(format *parser.JSONFormat) string {
	if format == nil {
		return ""
	}
	text := " FORMAT JSON"
	if format.Encoding != "" {
		text += " ENCODING " + format.Encoding
	}
	return text
}
