package sqltype

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/sqlstate"
)

// IntarraySetOperation identifies a released overload, never an operator spelling.
type IntarraySetOperation uint8

const (
	IntarrayOverlap IntarraySetOperation = iota
	IntarrayContains
	IntarrayContained
	IntarrayPushElem
	IntarrayPushArray
	IntarrayDeleteElem
	IntarraySubtract
	IntarrayUnionElem
	IntarrayUnion
	IntarrayIntersect
	IntarraySetOperationCount
)

func (op IntarraySetOperation) Validate(left, right, result *Descriptor) error {
	if op >= IntarraySetOperationCount || left == nil || right == nil || result == nil || left.ID() != 1007 || !left.Concrete() || !left.IsArray() || left.Element().ID() != Int4 {
		return fmt.Errorf("invalid intarray set signature")
	}
	scalar := op == IntarrayPushElem || op == IntarrayDeleteElem || op == IntarrayUnionElem
	if scalar && right != left.Element() || !scalar && right != left {
		return fmt.Errorf("invalid intarray set right formal")
	}
	if op <= IntarrayContained {
		if result.ID() != Bool || !result.Concrete() {
			return fmt.Errorf("invalid intarray boolean result")
		}
	} else if result != left {
		return fmt.Errorf("invalid intarray array result")
	}
	return nil
}

func validateIntarray(ctx context.Context, value Value) error {
	if value.array == nil {
		return fmt.Errorf("missing intarray payload")
	}
	for _, entry := range value.array.elements {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.typ != value.typ.Element() {
			return fmt.Errorf("foreign intarray element generation")
		}
		if entry.null {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22004", Message: "array must not contain nulls"}
		}
	}
	return nil
}

// Eval borrows immutable operands; every constructed result owns its buffers.
func (op IntarraySetOperation) Eval(ctx context.Context, left, right Value, leftType, rightType, result *Descriptor) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if err := op.Validate(leftType, rightType, result); err != nil {
		return Value{}, err
	}
	if left.typ != leftType || right.typ != rightType {
		return Value{}, fmt.Errorf("foreign intarray operand generation")
	}
	if left.null || right.null {
		return Null(result), nil
	}
	if err := validateIntarray(ctx, left); err != nil {
		return Value{}, err
	}
	if rightType.IsArray() {
		if err := validateIntarray(ctx, right); err != nil {
			return Value{}, err
		}
	}
	flat := func(elements []Value) (Value, error) {
		var dimensions []ArrayDimension
		if len(elements) != 0 {
			dimensions = []ArrayDimension{{Lower: 1, Length: int32(len(elements))}}
		}
		return OwnedArrayValue(result, dimensions, elements)
	}
	if op == IntarrayPushElem || op == IntarrayPushArray {
		n := 1
		if rightType.IsArray() {
			n = right.ArrayLen()
		}
		elements := make([]Value, left.ArrayLen()+n)
		copy(elements, left.array.elements)
		if rightType.IsArray() {
			copy(elements[left.ArrayLen():], right.array.elements)
		} else {
			elements[left.ArrayLen()] = right
		}
		return flat(elements)
	}
	if op == IntarrayDeleteElem {
		count := 0
		for _, v := range left.array.elements {
			if v.integer != right.integer {
				count++
			}
		}
		if count == left.ArrayLen() {
			return left, nil
		}
		elements := make([]Value, 0, count)
		for _, v := range left.array.elements {
			if v.integer != right.integer {
				elements = append(elements, v)
			}
		}
		var dimensions []ArrayDimension
		if count != 0 {
			dimensions = make([]ArrayDimension, left.ArrayRank())
			for i := range dimensions {
				dimensions[i] = left.ArrayDimension(i)
				dimensions[i].Length = 1
			}
			dimensions[0].Length = int32(count)
		}
		return OwnedArrayValue(result, dimensions, elements)
	}
	// NULL-element validation above precedes even predetermined empty answers.
	if op <= IntarrayContained && (left.ArrayLen() == 0 || right.ArrayLen() == 0) {
		answer := false
		if op == IntarrayContains {
			answer = right.ArrayLen() == 0
		}
		if op == IntarrayContained {
			answer = left.ArrayLen() == 0
		}
		return Boolean(result, answer), nil
	}
	if op == IntarrayIntersect && (left.ArrayLen() == 0 || right.ArrayLen() == 0) || op == IntarraySubtract && left.ArrayLen() == 0 {
		return flat(nil)
	}
	sorted := func(v Value) []int32 {
		data := make([]int32, v.ArrayLen())
		for i, entry := range v.array.elements {
			data[i] = int32(entry.integer)
		}
		slices.Sort(data)
		return slices.Compact(data)
	}
	a := sorted(left)
	var b []int32
	if rightType.IsArray() {
		b = sorted(right)
	} else {
		b = []int32{int32(right.integer)}
	}
	if op == IntarrayContained {
		a, b = b, a
	}
	if op <= IntarrayContained {
		i, j := 0, 0
		for i < len(a) && j < len(b) {
			if a[i] < b[j] {
				i++
				continue
			}
			if a[i] > b[j] {
				if op != IntarrayOverlap {
					return Boolean(result, false), nil
				}
				j++
				continue
			}
			if op == IntarrayOverlap {
				return Boolean(result, true), nil
			}
			i++
			j++
		}
		return Boolean(result, op != IntarrayOverlap && j == len(b)), nil
	}
	capacity := len(a) + len(b)
	if op == IntarraySubtract {
		capacity = len(a)
	}
	if op == IntarrayIntersect {
		capacity = min(len(a), len(b))
	}
	elements := make([]Value, 0, capacity)
	emit := func(n int32) { elements = append(elements, Value{typ: result.Element(), integer: int64(n)}) }
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		if j == len(b) || i < len(a) && a[i] < b[j] {
			if op != IntarrayIntersect {
				emit(a[i])
			}
			i++
		} else if i == len(a) || b[j] < a[i] {
			if op == IntarrayUnion || op == IntarrayUnionElem {
				emit(b[j])
			}
			j++
		} else {
			if op != IntarraySubtract {
				emit(a[i])
			}
			i++
			j++
		}
	}
	return flat(elements)
}
