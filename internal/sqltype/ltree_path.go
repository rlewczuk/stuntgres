package sqltype

import (
 "context"
 "fmt"
 "strings"
 "stuntgres/internal/sqlstate"
)

// LtreePathOperation identifies one exact installed overload, not a SQL name.
type LtreePathOperation uint8
const (
 LtreeSubltree LtreePathOperation = iota
 LtreeSubpathLength
 LtreeSubpath
 LtreeIndex
 LtreeIndexOffset
 LtreeNlevel
 LtreeToText
 LtreeFromText
 LtreeLCAArray
 LtreeLCA2
 LtreeLCA3
 LtreeLCA4
 LtreeLCA5
 LtreeLCA6
 LtreeLCA7
 LtreeLCA8
 LtreeConcat
 LtreeConcatText
 LtreeTextConcat
 LtreePathOperationCount
)

func (op LtreePathOperation) Arity() int {
 switch op {
 case LtreeSubltree, LtreeSubpathLength, LtreeIndexOffset: return 3
 case LtreeSubpath, LtreeIndex, LtreeConcat, LtreeConcatText, LtreeTextConcat: return 2
 case LtreeNlevel, LtreeToText, LtreeFromText, LtreeLCAArray: return 1
 }
 if op >= LtreeLCA2 && op <= LtreeLCA8 { return int(op-LtreeLCA2)+2 }
 return 0
}

// Validate authenticates all concrete formal generations, including the result.
func (op LtreePathOperation) Validate(formals [8]*Descriptor, count int, result *Descriptor) error {
 invalid := func() error { return fmt.Errorf("invalid ltree path formal signature") }
 if count == 0 || count != op.Arity() || result == nil || !result.Concrete() { return invalid() }
 var base *Descriptor
 for i, formal := range formals {
  if i >= count { if formal != nil { return invalid() }; continue }
  if formal == nil || !formal.Concrete() { return invalid() }
  want := byte('l')
  switch op {
  case LtreeSubltree, LtreeSubpathLength, LtreeSubpath: if i > 0 { want = 'i' }
  case LtreeIndexOffset: if i == 2 { want = 'i' }
  case LtreeFromText: want = 't'
  case LtreeLCAArray: want = 'a'
  case LtreeConcatText: if i == 1 { want = 't' }
  case LtreeTextConcat: if i == 0 { want = 't' }
  }
  switch want {
  case 'i': if formal.ID() != Int4 { return invalid() }
  case 't': if formal.ID() != Text { return invalid() }
  case 'a': if !formal.IsArray() || !formal.Element().IsLtree() { return invalid() }; base = formal.Element()
  case 'l': if !formal.IsLtree() || base != nil && base != formal { return invalid() }; base = formal
  }
 }
 switch op {
 case LtreeIndex, LtreeIndexOffset, LtreeNlevel: if result.ID() != Int4 { return invalid() }
 case LtreeToText: if result.ID() != Text { return invalid() }
 case LtreeFromText: if !result.IsLtree() { return invalid() }
 default: if base != result { return invalid() }
 }
 return nil
}

func pathFailure(code, message string) error { return &sqlstate.Error{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:code, Message:message} }

func (op LtreePathOperation) Eval(ctx context.Context, values [8]Value, formals [8]*Descriptor, count int, result *Descriptor) (Value, error) {
 if err := ctx.Err(); err != nil { return Value{}, err }
 if err := op.Validate(formals, count, result); err != nil { return Value{}, err }
 null := false
 for i, value := range values[:count] {
  if value.typ != formals[i] { return Value{}, fmt.Errorf("foreign ltree path operand generation") }
  if !value.null && (value.typ.IsLtree() && !value.validLtree() || value.typ.IsArray() && value.array == nil) { return Value{}, fmt.Errorf("invalid ltree path payload") }
  null = null || value.null
 }
 if null { return Null(result), nil }
 a := values[0]
 switch op {
 case LtreeNlevel: return Integer(result, int64(len(a.ltree.labels)))
 case LtreeToText: return String(result, a.ltree.text), nil
 case LtreeFromText: return result.ltreeInput(ctx, a.text)
 case LtreeSubltree, LtreeSubpathLength, LtreeSubpath:
  start, end := int32(values[1].integer), int32(len(a.ltree.labels))
  if op == LtreeSubltree { end = int32(values[2].integer) } else {
   if start < 0 { start = int32(len(a.ltree.labels)) + start }
   length := int32(0)
   if op == LtreeSubpathLength { length = int32(values[2].integer) }
   // REL_18_6 deliberately adjusts a still-negative start a second time.
   if start < 0 { start = int32(len(a.ltree.labels)) + start }
   if length > 0 { end = start + length } else if length < 0 { end = int32(len(a.ltree.labels)) + length } else if op == LtreeSubpathLength { end = start }
  }
  if start < 0 || end < 0 || start >= int32(len(a.ltree.labels)) || start > end { return Value{}, pathFailure("22023", "invalid positions") }
  if end > int32(len(a.ltree.labels)) { end = int32(len(a.ltree.labels)) }
  return ltreePathSlice(ctx, a, int(start), int(end))
 case LtreeIndex, LtreeIndexOffset:
  b := values[1]
  if len(a.ltree.labels) == 0 || len(b.ltree.labels) == 0 { return Integer(result, -1) }
  offset := int32(0)
  depth := int32(len(a.ltree.labels))
  if op == LtreeIndexOffset {
   offset = int32(values[2].integer)
   if offset < 0 {
    // Release arithmetic negates and subtracts in int32. INT_MIN therefore
    // reaches the wrapped remaining-length rejection, not the ordinary clamp.
    if -offset >= depth { offset = 0 } else { offset = depth + offset }
   }
  }
  if depth-offset < int32(len(b.ltree.labels)) { return Integer(result, -1) }
  for i := int(offset); i <= len(a.ltree.labels)-len(b.ltree.labels); i++ {
   if err := ctx.Err(); err != nil { return Value{}, err }
   first, last := a.ltree.labels[i], a.ltree.labels[i+len(b.ltree.labels)-1]
   equal, err := labelEqual(ctx, a.ltree.text[first.start:last.end], b.ltree.text, 0)
   if err != nil { return Value{}, err }; if equal { return Integer(result, int64(i)) }
  }
  return Integer(result, -1)
 case LtreeLCAArray:
  if err := validateLtreeArray(ctx, a); err != nil { return Value{}, err }
  return ltreePathLCA(ctx, a.array.elements, result)
 case LtreeConcat, LtreeConcatText, LtreeTextConcat:
  b := values[1]
  var err error
  if op == LtreeConcatText { b, err = result.ltreeInput(ctx, b.text) }
  if op == LtreeTextConcat { a, err = result.ltreeInput(ctx, a.text) }
  if err != nil { return Value{}, err }
  return ltreePathConcat(ctx, a, b)
 default: return ltreePathLCA(ctx, values[:count], result)
 }
}

// Slice owns its rebased spans and text, sharing only an exact whole-path result.
func ltreePathSlice(ctx context.Context, path Value, start, end int) (Value, error) {
 if start == 0 && end == len(path.ltree.labels) { return path, nil }
 if start == end { return Value{typ:path.typ, ltree:&ltreePayload{}}, nil }
 offset := path.ltree.labels[start].start
 labels := make([]labelSpan, end-start)
 for i := range labels {
  if err := ctx.Err(); err != nil { return Value{}, err }
  span := path.ltree.labels[start+i]; labels[i] = labelSpan{span.start-offset, span.end-offset}
 }
 text := strings.Clone(path.ltree.text[offset:path.ltree.labels[end-1].end])
 return Value{typ:path.typ, ltree:&ltreePayload{text:text, labels:labels}}, nil
}

func ltreePathLCA(ctx context.Context, paths []Value, result *Descriptor) (Value, error) {
 if len(paths) == 0 { return Null(result), nil }
 common := len(paths[0].ltree.labels)-1
 if common < 0 { return Null(result), nil }
 first := paths[0].ltree
 for _, path := range paths[1:] {
  if err := ctx.Err(); err != nil { return Value{}, err }
  if len(path.ltree.labels) == 0 { return Null(result), nil }
  if n := len(path.ltree.labels)-1; n < common { common = n }
  for i := range common {
   a, b := first.labels[i], path.ltree.labels[i]
   equal, err := labelEqual(ctx, first.text[a.start:a.end], path.ltree.text[b.start:b.end], 0)
   if err != nil { return Value{}, err }; if !equal { common = i; break }
  }
 }
 return ltreePathSlice(ctx, paths[0], 0, common)
}

func ltreePathConcat(ctx context.Context, a, b Value) (Value, error) {
 count := len(a.ltree.labels)+len(b.ltree.labels)
 if count > 65535 { return Value{}, pathFailure("54000", fmt.Sprintf("number of ltree levels (%d) exceeds the maximum allowed (65535)", count)) }
 if len(a.ltree.labels) == 0 { return b, nil }; if len(b.ltree.labels) == 0 { return a, nil }
 var text strings.Builder
 text.Grow(len(a.ltree.text)+len(b.ltree.text)+1)
 text.WriteString(a.ltree.text); text.WriteByte('.'); text.WriteString(b.ltree.text)
 labels := make([]labelSpan, count)
 for i, span := range a.ltree.labels { if err := ctx.Err(); err != nil { return Value{}, err }; labels[i] = span }
 offset := len(a.ltree.text)+1
 for i, span := range b.ltree.labels { if err := ctx.Err(); err != nil { return Value{}, err }; labels[len(a.ltree.labels)+i] = labelSpan{span.start+offset, span.end+offset} }
 return Value{typ:a.typ, ltree:&ltreePayload{text:text.String(), labels:labels}}, nil
}
