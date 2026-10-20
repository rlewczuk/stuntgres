package analyze

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *Binder) bindMergeAction(raw parser.MergeActionExpr) (expr.Expr, error) {
	frame := uint32(0)
	if b.Target.returningAliases != nil && b.Target.returningAliases.mode == returningMerge {
		frame = b.FrameID()
	} else {
		for i := len(b.Query.Scopes) - 1; i >= 0; i-- {
			s := &b.Query.Scopes[i]
			if s.returning != nil && s.returning.mode == returningMerge {
				frame = s.frame
				break
			}
		}
	}
	if frame == 0 {
		return nil, expr.ErrorAt("42601", "MERGE_ACTION() can only be used in the RETURNING list of a MERGE command", raw.Pos)
	}
	return plan.MergeActionExpr{Info: b.Info(b.Type(sqltype.Text).Usage(), raw.Pos, expr.Properties{RowDependent: true}), Scope: 1, Frame: frame}, nil
}
func (b *Binder) mergeReturningFrame(frame uint32) bool {
	if frame == b.FrameID() && b.Target.returningAliases != nil && b.Target.returningAliases.mode == returningMerge {
		return true
	}
	for i := len(b.Query.Scopes) - 1; i >= 0; i-- {
		s := &b.Query.Scopes[i]
		if s.frame == frame && s.returning != nil && s.returning.mode == returningMerge {
			return true
		}
	}
	return false
}
