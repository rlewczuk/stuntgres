package sqltype

import (
 "context"
 "fmt"
)

// LtreeLqueryOperation identifies a selected scalar positional match overload.
type LtreeLqueryOperation uint8

const (
 LtreeLqueryMatch LtreeLqueryOperation = iota
 LtreeLqueryReverseMatch
 LtreeLqueryOperationCount
)

func (op LtreeLqueryOperation) Validate(left, right, result *Descriptor) error {
 if op >= LtreeLqueryOperationCount || left == nil || right == nil || !left.Concrete() || !right.Concrete() || result == nil || result.ID() != Bool { return fmt.Errorf("invalid ltree lquery formal signature") }
 if op == LtreeLqueryReverseMatch { left, right = right, left }
 if !left.IsLtree() || !right.IsLquery() { return fmt.Errorf("invalid ltree lquery formal signature") }
 return nil
}

func (op LtreeLqueryOperation) Eval(ctx context.Context, left, right Value, leftType, rightType, result *Descriptor) (Value, error) {
 if err := ctx.Err(); err != nil { return Value{}, err }
 if err := op.Validate(leftType, rightType, result); err != nil { return Value{}, err }
 if left.typ != leftType || right.typ != rightType { return Value{}, fmt.Errorf("foreign ltree lquery operand generation") }
 if op == LtreeLqueryReverseMatch { left, right = right, left }
 if !left.null && !left.validLtree() || !right.null && !right.validLquery() { return Value{}, fmt.Errorf("invalid ltree lquery payload") }
 if left.null || right.null { return Null(result), nil }
 var scratch ltreeMatchScratch
 matched, err := scratch.query(ctx, left.ltree, right.lquery)
 if err != nil { return Value{}, err }
 return Boolean(result, matched), nil
}
