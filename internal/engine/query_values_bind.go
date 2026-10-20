package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

// VALUES ordinals follow structural source preorder, independent of physical
// table occurrences and source offsets. Row/key indices are local to a source.
func (b *expressionBinder) captureValuesViewLiteral(raw parser.Expr, bound expr.Expr, clause viewLiteralClause, source, row, column int) error {
	path := viewLiteralPath{Query: b.FrameID(), Clause: clause, Source: source, Row: row, Target: column}
	if clause == analyze.ViewValuesOrderLiteral {
		if integer, ok := raw.(parser.LiteralExpr); ok && integer.Value.Kind == parser.IntegerValue {
			return nil
		}
	}
	ordinal := 0
	return b.CaptureViewLiteralTree(raw, bound, path, &ordinal)
}
