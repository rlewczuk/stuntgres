package sqltype

import (
 "context"
 "fmt"
)

// LtreeHierarchyOperation identifies a selected scalar containment overload.
type LtreeHierarchyOperation uint8

const (
 LtreeAncestor LtreeHierarchyOperation = iota
 LtreeDescendant
 LtreeHierarchyOperationCount
)

func (op LtreeHierarchyOperation) Validate(left, right, result *Descriptor) error {
 if op >= LtreeHierarchyOperationCount || left == nil || !left.Concrete() || !left.IsLtree() || right != left || result == nil || result.ID() != Bool {
  return fmt.Errorf("invalid ltree hierarchy formal signature")
 }
 return nil
}

func (op LtreeHierarchyOperation) Eval(ctx context.Context, left, right Value, leftType, rightType, result *Descriptor) (Value, error) {
 if err := ctx.Err(); err != nil { return Value{}, err }
 if err := op.Validate(leftType, rightType, result); err != nil { return Value{}, err }
 if left.typ != leftType || right.typ != rightType { return Value{}, fmt.Errorf("foreign ltree hierarchy operand generation") }
 if !left.null && !left.validLtree() || !right.null && !right.validLtree() { return Value{}, fmt.Errorf("invalid ltree hierarchy payload") }
 if left.null || right.null { return Null(result), nil }
 if op == LtreeDescendant { return Boolean(result, ltreeAncestor(right.ltree, left.ltree)), nil }
 return Boolean(result, ltreeAncestor(left.ltree, right.ltree)), nil
}
