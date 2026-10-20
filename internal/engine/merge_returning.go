package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindMergeAction(raw parser.MergeActionExpr) (expr.Expr, error) {
	frame := uint32(0)
	if b.target.returningAliases != nil && b.target.returningAliases.mode == returningMerge {
		frame = b.FrameID()
	} else {
		for i := len(b.query.scopes) - 1; i >= 0; i-- {
			s := &b.query.scopes[i]
			if s.returning != nil && s.returning.mode == returningMerge {
				frame = s.frame
				break
			}
		}
	}
	if frame == 0 {
		return nil, expr.ErrorAt("42601", "MERGE_ACTION() can only be used in the RETURNING list of a MERGE command", raw.Pos)
	}
	return plan.MergeActionExpr{Info: b.info(b.typ(sqltype.Text).Usage(), raw.Pos, expr.Properties{RowDependent: true}), Scope: 1, Frame: frame}, nil
}
func (b *expressionBinder) mergeReturningFrame(frame uint32) bool {
	if frame == b.FrameID() && b.target.returningAliases != nil && b.target.returningAliases.mode == returningMerge {
		return true
	}
	for i := len(b.query.scopes) - 1; i >= 0; i-- {
		s := &b.query.scopes[i]
		if s.frame == frame && s.returning != nil && s.returning.mode == returningMerge {
			return true
		}
	}
	return false
}
