package sqltype

import (
	"context"
	"fmt"
)

// IntarraySliceOperation identifies one released overload, including its arity.
type IntarraySliceOperation uint8

const (
	IntarrayIcount IntarraySliceOperation = iota
	IntarrayIdx
	IntarraySubarray
	IntarraySubarrayLength
	IntarrayIntset
	IntarraySliceOperationCount
)

func (op IntarraySliceOperation) Arity() int {
	switch op {
	case IntarrayIcount, IntarrayIntset:
		return 1
	case IntarrayIdx, IntarraySubarray:
		return 2
	case IntarraySubarrayLength:
		return 3
	}
	return 0
}

func (op IntarraySliceOperation) Validate(formals []*Descriptor, result *Descriptor) error {
	invalid := func() error { return fmt.Errorf("invalid intarray slice signature") }
	if op.Arity() == 0 || len(formals) != op.Arity() || result == nil { return invalid() }
	for _, formal := range formals {
		if formal == nil || !formal.Concrete() { return invalid() }
	}
	array, integer := formals[0], result
	if op == IntarrayIntset {
		array, integer = result, formals[0]
	} else if len(formals) > 1 {
		integer = formals[1]
	}
	if !array.Concrete() || array.ID() != 1007 || !array.IsArray() || integer.ID() != Int4 || !integer.Concrete() || array.Element() != integer { return invalid() }
	for _, formal := range formals[1:] {
		if formal != integer { return invalid() }
	}
	if op == IntarrayIcount || op == IntarrayIdx {
		if result != integer { return invalid() }
	} else if result != array { return invalid() }
	return nil
}

// Eval borrows immutable operands. Count/search allocate no scratch; constructed
// arrays own their flat lower-one shape and exactly one element buffer.
func (op IntarraySliceOperation) Eval(ctx context.Context, arguments []Value, formals []*Descriptor, result *Descriptor) (Value, error) {
	if err := ctx.Err(); err != nil { return Value{}, err }
	if err := op.Validate(formals, result); err != nil { return Value{}, err }
	if len(arguments) != len(formals) { return Value{}, fmt.Errorf("invalid intarray slice arguments") }
	anyNull := false
	for i, argument := range arguments {
		if argument.typ != formals[i] { return Value{}, fmt.Errorf("foreign intarray slice operand generation") }
		anyNull = anyNull || argument.null
	}
	if anyNull { return Null(result), nil }
	if op == IntarrayIntset {
		return OwnedArrayValue(result, []ArrayDimension{{Lower: 1, Length: 1}}, []Value{arguments[0]})
	}
	array := arguments[0]
	if array.array == nil { return Value{}, fmt.Errorf("missing intarray payload") }
	if op == IntarrayIcount {
		return Integer(result, int64(array.ArrayLen()))
	}
	// Full validation precedes search matches and even empty slice shortcuts.
	if err := validateIntarray(ctx, array); err != nil { return Value{}, err }
	if op == IntarrayIdx {
		for i, element := range array.array.elements {
			if err := ctx.Err(); err != nil { return Value{}, err }
			if element.integer == arguments[1].integer { return Integer(result, int64(i+1)) }
		}
		return Integer(result, 0)
	}
	count := int32(array.ArrayLen())
	start := int32(arguments[1].integer)
	if start > 0 { start-- }
	if start < 0 { start = count + start }
	length := int32(0)
	if op == IntarraySubarrayLength { length = int32(arguments[2].integer) }
	end := count
	if length < 0 {
		end = count + length
	} else if length > 0 {
		// PostgreSQL 18.6 wraps this signed-int32 sum; widening would change
		// the independently observed INT4_MAX length boundaries.
		end = start + length
	}
	if end > count { end = count }
	if start < 0 { start = 0 }
	if start >= end || end <= 0 { return OwnedArrayValue(result, nil, nil) }
	elements := make([]Value, int(end-start))
	for i := range elements {
		if err := ctx.Err(); err != nil { return Value{}, err }
		elements[i] = array.ArrayElement(int(start)+i)
	}
	return OwnedArrayValue(result, []ArrayDimension{{Lower: 1, Length: end-start}}, elements)
}
