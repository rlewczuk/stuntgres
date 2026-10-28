package sqltype

import (
 "context"
 "fmt"
 "stuntgres/internal/sqlstate"
)

// LtreeArrayOperation is the stable selected-overload role, independent of OIDs.
type LtreeArrayOperation uint8
const (
 LtreeArrayAncestor LtreeArrayOperation = iota
 LtreeArrayAncestorReverse
 LtreeArrayDescendant
 LtreeArrayDescendantReverse
 LtreeArrayQuery
 LtreeArrayQueryReverse
 LtreeQueryArray
 LtreeQueryArrayReverse
 LtreeArraysQuery
 LtreeArraysQueryReverse
 LtreeArrayTextQuery
 LtreeArrayTextQueryReverse
 LtreeArrayFirstAncestor
 LtreeArrayFirstDescendant
 LtreeArrayFirstQuery
 LtreeArrayFirstTextQuery
 LtreeArrayOperationCount
)

func (op LtreeArrayOperation) Validate(left, right, result *Descriptor) error {
 array := func(d *Descriptor, query bool) bool { return d != nil && d.Concrete() && d.IsArray() && ((!query && d.Element().IsLtree()) || (query && d.Element().IsLquery())) }
 if op >= LtreeArrayOperationCount || left == nil || right == nil || result == nil { return fmt.Errorf("invalid ltree array operation") }
 a, b := left, right
 if op < LtreeArrayFirstAncestor && op%2 == 1 { a, b = b, a }
 valid := false
 switch op {
 case LtreeArrayAncestor, LtreeArrayAncestorReverse, LtreeArrayDescendant, LtreeArrayDescendantReverse, LtreeArrayFirstAncestor, LtreeArrayFirstDescendant:
  valid = array(a, false) && b.IsLtree() && a.Element() == b
 case LtreeArrayQuery, LtreeArrayQueryReverse, LtreeArrayFirstQuery: valid = array(a, false) && b.IsLquery()
 case LtreeQueryArray, LtreeQueryArrayReverse: valid = a.IsLtree() && array(b, true)
 case LtreeArraysQuery, LtreeArraysQueryReverse: valid = array(a, false) && array(b, true)
 case LtreeArrayTextQuery, LtreeArrayTextQueryReverse, LtreeArrayFirstTextQuery: valid = array(a, false) && b.IsLtxtquery()
 }
 if op >= LtreeArrayFirstAncestor { valid = valid && result == a.Element() } else { valid = valid && result.ID() == Bool }
 if !valid { return fmt.Errorf("invalid ltree array formal signature") }; return nil
}

func validateLtreeArray(ctx context.Context, value Value) error {
 if value.array == nil { return fmt.Errorf("missing ltree array payload") }
 if value.ArrayRank() > 1 { return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2202E", Message: "array must be one-dimensional"} }
 for _, entry := range value.array.elements {
  if err := ctx.Err(); err != nil { return err }
  if entry.typ != value.typ.Element() { return fmt.Errorf("foreign ltree array element") }
  if entry.null { return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22004", Message: "array must not contain nulls"} }
  if entry.typ.IsLtree() && !entry.validLtree() || entry.typ.IsLquery() && !entry.validLquery() { return fmt.Errorf("invalid ltree array element payload") }
 }
 return nil
}

func (op LtreeArrayOperation) Eval(ctx context.Context, left, right Value, leftType, rightType, result *Descriptor) (Value, error) {
 if err := ctx.Err(); err != nil { return Value{}, err }
 if err := op.Validate(leftType, rightType, result); err != nil { return Value{}, err }
 if left.typ != leftType || right.typ != rightType { return Value{}, fmt.Errorf("foreign ltree array operand generation") }
 if left.null || right.null { return Null(result), nil }
 a, b := left, right
 if op < LtreeArrayFirstAncestor && op%2 == 1 { a, b = b, a }
 if a.typ.IsLtree() && !a.validLtree() || b.typ.IsLtree() && !b.validLtree() || b.typ.IsLquery() && !b.validLquery() || b.typ.IsLtxtquery() && !b.validLtxtquery() { return Value{}, fmt.Errorf("invalid ltree operation payload") }
 var scratch ltreeMatchScratch
 if op >= LtreeQueryArray && op <= LtreeArraysQueryReverse {
  if err := validateLtreeArray(ctx, b); err != nil { return Value{}, err }
  if b.ArrayLen() == 0 { return Boolean(result, false), nil }
  if a.typ.IsArray() { if err := validateLtreeArray(ctx, a); err != nil { return Value{}, err } }
  for _, query := range b.array.elements {
   if a.typ.IsArray() {
    for _, path := range a.array.elements {
     matched, err := scratch.query(ctx, path.ltree, query.lquery)
     if err != nil { return Value{}, err }; if matched { return Boolean(result, true), nil }
    }
   } else {
    matched, err := scratch.query(ctx, a.ltree, query.lquery)
    if err != nil { return Value{}, err }; if matched { return Boolean(result, true), nil }
   }
  }
  return Boolean(result, false), nil
 }
 if err := validateLtreeArray(ctx, a); err != nil { return Value{}, err }
 for _, path := range a.array.elements {
  if err := ctx.Err(); err != nil { return Value{}, err }
  var matched bool
  var err error
  switch op {
  case LtreeArrayAncestor, LtreeArrayAncestorReverse, LtreeArrayFirstAncestor: matched = ltreeAncestor(path.ltree, b.ltree)
  case LtreeArrayDescendant, LtreeArrayDescendantReverse, LtreeArrayFirstDescendant: matched = ltreeAncestor(b.ltree, path.ltree)
  case LtreeArrayQuery, LtreeArrayQueryReverse, LtreeArrayFirstQuery: matched, err = scratch.query(ctx, path.ltree, b.lquery)
  case LtreeArrayTextQuery, LtreeArrayTextQueryReverse, LtreeArrayFirstTextQuery: matched, err = scratch.textQuery(ctx, path.ltree, b.ltxtquery)
  }
  if err != nil { return Value{}, err }
  if matched { if op >= LtreeArrayFirstAncestor { return path, nil }; return Boolean(result, true), nil }
 }
 if op >= LtreeArrayFirstAncestor { return Null(result), nil }; return Boolean(result, false), nil
}
