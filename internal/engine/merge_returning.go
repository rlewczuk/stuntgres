package engine

import (
	"errors"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The owner is lexical, not whichever query is currently evaluating a child.
type mergeActionExpression struct {
	expressionInfo
	scope, frame uint32
}

func (e mergeActionExpression) Eval(ctx ExprContext) (sqltype.Value, error) {
	c := evalContext(ctx)
	owner, err := returningRowContext(c, e.scope, e.frame)
	if err != nil {
		return sqltype.Value{}, err
	}
	if !owner.mergeReturning {
		return sqltype.Value{}, errors.New("stuntgres: MERGE output action is missing")
	}
	var text string
	switch owner.mergeAction {
	case parser.MergeInsertAction:
		text = "INSERT"
	case parser.MergeUpdateAction:
		text = "UPDATE"
	case parser.MergeDeleteAction:
		text = "DELETE"
	default:
		return sqltype.Value{}, errors.New("stuntgres: invalid MERGE output action")
	}
	return sqltype.String(e.Usage.Type, text), nil
}
func (b *expressionBinder) bindMergeAction(raw parser.MergeActionExpr) (BoundExpr, error) {
	frame := uint32(0)
	if b.returningAliases != nil && b.returningAliases.mode == returningMerge {
		frame = b.frameID()
	} else {
		for i := len(b.queryScopes) - 1; i >= 0; i-- {
			s := &b.queryScopes[i]
			if s.returning != nil && s.returning.mode == returningMerge {
				frame = s.frame
				break
			}
		}
	}
	if frame == 0 {
		return nil, expressionError("42601", "MERGE_ACTION() can only be used in the RETURNING list of a MERGE command", raw.Pos)
	}
	return mergeActionExpression{expressionInfo: b.info(b.typ(sqltype.Text).Usage(), raw.Pos, ExprProperties{RowDependent: true}), scope: 1, frame: frame}, nil
}
func (b *expressionBinder) mergeReturningFrame(frame uint32) bool {
	if frame == b.frameID() && b.returningAliases != nil && b.returningAliases.mode == returningMerge {
		return true
	}
	for i := len(b.queryScopes) - 1; i >= 0; i-- {
		s := &b.queryScopes[i]
		if s.frame == frame && s.returning != nil && s.returning.mode == returningMerge {
			return true
		}
	}
	return false
}

type mergeReturningSide uint8

const (
	mergeDefaultSide mergeReturningSide = iota
	mergeOldSide
	mergeNewSide
)

func mergeReturningAbsent(c *EvalContext, side mergeReturningSide) bool {
	return side == mergeOldSide && c.mergeAction == parser.MergeInsertAction || side == mergeNewSide && c.mergeAction == parser.MergeDeleteAction
}

// This execution-private collector never escapes before executeMerge's staging
// and the request's sole Finish. It reuses one full target-first/source frame.
type mergeReturningOutput struct {
	returning *boundReturning
	rows      *finalRows
	frame     []sqltype.Value
}

func newMergeReturningOutput(b binding, s *StatementScope) *mergeReturningOutput {
	if b.returning == nil {
		return nil
	}
	rows := newFinalRows(b.returning.schema, 0)
	rows.output = s.output
	return &mergeReturningOutput{returning: b.returning, rows: rows, frame: make([]sqltype.Value, b.merge.targetWidth+b.merge.sourceWidth)}
}
func (o *mergeReturningOutput) project(b binding, c *EvalContext, action parser.MergeAction, old, row, source []sqltype.Value, sourcePresent bool) error {
	if o == nil {
		return nil
	}
	evaluation := *c
	evaluation.Scope, evaluation.Frame = 1, 1
	evaluation.mergeReturning, evaluation.mergeAction, evaluation.mergeSourcePresent = true, action, sourcePresent
	evaluation.Row, evaluation.OldRow = o.frame, old
	evaluation.physicalTable = b.ref
	if action == parser.MergeDeleteAction {
		row = old
	}
	copy(o.frame, row)
	copy(o.frame[b.merge.targetWidth:], source)
	for i, expr := range o.returning.expressions {
		value, err := expr.Eval(&evaluation)
		if err != nil {
			return err
		}
		if value.Type() == nil || !value.Type().Concrete() || value.Type() != o.rows.schema[i].Usage.Type {
			return errors.New("stuntgres: invalid finalized MERGE value")
		}
		o.rows.values = append(o.rows.values, value)
	}
	o.rows.count++
	return nil
}
