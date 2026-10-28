package sqltype

import (
 "context"
 "fmt"
)

// LtreeLtxtqueryOperation identifies the selected mixed scalar signature.
type LtreeLtxtqueryOperation uint8

const (
 LtreeLtxtqueryForward LtreeLtxtqueryOperation = iota
 LtreeLtxtqueryReverse
 LtreeLtxtqueryOperationCount
)

func (op LtreeLtxtqueryOperation) Validate(left, right, result *Descriptor) error {
 if op >= LtreeLtxtqueryOperationCount || left == nil || right == nil || !left.Concrete() || !right.Concrete() || result == nil || result.ID() != Bool { return fmt.Errorf("invalid ltree ltxtquery formal signature") }
 if op == LtreeLtxtqueryReverse { left, right = right, left }
 if !left.IsLtree() || !right.IsLtxtquery() { return fmt.Errorf("invalid ltree ltxtquery formal signature") }
 return nil
}

func (op LtreeLtxtqueryOperation) Eval(ctx context.Context, left, right Value, leftType, rightType, result *Descriptor) (Value, error) {
 if err := ctx.Err(); err != nil { return Value{}, err }
 if err := op.Validate(leftType, rightType, result); err != nil { return Value{}, err }
 if left.typ != leftType || right.typ != rightType { return Value{}, fmt.Errorf("foreign ltree ltxtquery operand generation") }
 if op == LtreeLtxtqueryReverse { left, right = right, left }
 if !left.null && !left.validLtree() || !right.null && !right.validLtxtquery() { return Value{}, fmt.Errorf("invalid ltree ltxtquery payload") }
 if left.null || right.null { return Null(result), nil }
 var scratch ltreeMatchScratch
 matched, err := scratch.textQuery(ctx, left.ltree, right.ltxtquery)
 if err != nil { return Value{}, err }
 return Boolean(result, matched), nil
}
