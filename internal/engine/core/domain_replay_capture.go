package core

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
)

// This transport is operation-owned, immutable after construction, and only the
// exact INSERT's destination binder consumes it. It is never a session setting,
// generic parameter importer, or expression-binder exception.
type DomainReplayCapture struct {
	Statement *parser.Insert
	Relation  *catalog.Relation
	Cells     map[int]typedef.DomainReplayLiteral // actual explicit VALUES slot, not raw SQL offsets
}

func capturedDomainReplayCell(b *analyze.Binding, op *session.Operation, s *parser.Insert, row, column int) (expr.Expr, bool, error) {
	if op == nil || OperationReplayOrZero(op).DomainReplay == nil || OperationReplayOrZero(op).DomainReplay.Statement != s {
		return nil, false, nil
	}
	capture := OperationReplayOrZero(op).DomainReplay
	literal, ok := capture.Cells[column]
	if !ok {
		return nil, false, nil
	}
	if row != 0 || capture.Relation != b.Relation || column >= len(b.Targets) || literal.ResultUsage() != b.Relation.Columns[b.Targets[column]].Usage {
		return nil, true, fmt.Errorf("stuntgres: native domain replay destination changed")
	}
	return plan.LiteralExpr{Info: literal.Info, Value: literal.Value}, true, nil
}
