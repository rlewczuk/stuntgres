package sqltype

import (
	"context"
	"fmt"
	"sort"

	"stuntgres/internal/sqlstate"
)

// IntarraySortOperation identifies exactly one installed, released overload.
type IntarraySortOperation uint8

const (
	IntarraySort IntarraySortOperation = iota
	IntarraySortDirection
	IntarraySortAsc
	IntarraySortDesc
	IntarrayUniq
	IntarraySortOperationCount
)

func (op IntarraySortOperation) Arity() int {
	if op >= IntarraySortOperationCount {
		return 0
	}
	if op == IntarraySortDirection {
		return 2
	}
	return 1
}

func (op IntarraySortOperation) Validate(formals []*Descriptor, result *Descriptor) error {
	invalid := func() error { return fmt.Errorf("invalid intarray sort signature") }
	if op.Arity() == 0 || len(formals) != op.Arity() || result == nil {
		return invalid()
	}
	array := formals[0]
	if array == nil || !array.Concrete() || array.ID() != 1007 || !array.IsArray() || array.Element() == nil || !array.Element().Concrete() || array.Element().ID() != Int4 || result != array {
		return invalid()
	}
	if len(formals) == 2 && (formals[1] == nil || !formals[1].Concrete() || formals[1].ID() != Text) {
		return invalid()
	}
	return nil
}

// Eval borrows immutable inputs. Sorting owns one leaf buffer and preserves the
// shape; adjacent compaction allocates only when it actually removes leaves.
func (op IntarraySortOperation) Eval(ctx context.Context, arguments []Value, formals []*Descriptor, result *Descriptor) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if err := op.Validate(formals, result); err != nil {
		return Value{}, err
	}
	if len(arguments) != len(formals) {
		return Value{}, fmt.Errorf("invalid intarray sort arguments")
	}
	anyNull := false
	for i, argument := range arguments {
		if argument.typ != formals[i] {
			return Value{}, fmt.Errorf("foreign intarray sort operand generation")
		}
		anyNull = anyNull || argument.null
	}
	if anyNull {
		return Null(result), nil
	}
	array := arguments[0]
	if err := validateIntarray(ctx, array); err != nil {
		return Value{}, err
	}
	if array.ArrayLen() < 2 {
		return array, nil
	}
	if op == IntarrayUniq {
		return intarrayAdjacentUnique(ctx, array)
	}
	descending := op == IntarraySortDesc
	if op == IntarraySortDirection {
		direction := arguments[1].text
		asc := len(direction) == 3 && direction[0]|32 == 'a' && direction[1]|32 == 's' && direction[2]|32 == 'c'
		desc := len(direction) == 4 && direction[0]|32 == 'd' && direction[1]|32 == 'e' && direction[2]|32 == 's' && direction[3]|32 == 'c'
		if !asc && !desc {
			return Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: `second parameter must be "ASC" or "DESC"`}
		}
		descending = desc
	}
	elements := make([]Value, array.ArrayLen())
	for i, element := range array.array.elements {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		elements[i] = element
	}
	ordering := intarraySortOrder{ctx: ctx, elements: elements, descending: descending}
	sort.Sort(&ordering)
	if ordering.err != nil {
		return Value{}, ordering.err
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	return OwnedArrayValue(result, array.ArrayDimensions(), elements)
}

type intarraySortOrder struct {
	ctx        context.Context
	elements   []Value
	descending bool
	err        error
}

func (s *intarraySortOrder) Len() int { return len(s.elements) }
func (s *intarraySortOrder) Less(i, j int) bool {
	if s.err != nil {
		return false
	}
	if s.err = s.ctx.Err(); s.err != nil {
		return false
	}
	if s.descending {
		return s.elements[i].integer > s.elements[j].integer
	}
	return s.elements[i].integer < s.elements[j].integer
}
func (s *intarraySortOrder) Swap(i, j int) {
	if s.err != nil {
		return
	}
	if s.err = s.ctx.Err(); s.err != nil {
		return
	}
	s.elements[i], s.elements[j] = s.elements[j], s.elements[i]
}

func intarrayAdjacentUnique(ctx context.Context, array Value) (Value, error) {
	count := 1
	for i := 1; i < array.ArrayLen(); i++ {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		if array.array.elements[i].integer != array.array.elements[i-1].integer {
			count++
		}
	}
	if count == array.ArrayLen() {
		return array, nil
	}
	elements := make([]Value, count)
	elements[0] = array.array.elements[0]
	next := 1
	for i := 1; i < array.ArrayLen(); i++ {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		if array.array.elements[i].integer != array.array.elements[i-1].integer {
			elements[next] = array.array.elements[i]
			next++
		}
	}
	dimensions := array.ArrayDimensions()
	for i := range dimensions {
		dimensions[i].Length = 1
	}
	dimensions[0].Length = int32(count)
	return OwnedArrayValue(array.typ, dimensions, elements)
}
