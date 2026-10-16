package sqltype

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sort"

	"stuntgres/internal/sqlstate"
)

// ArrayPush inserts a typed element without exposing or mutating borrowed data.
func ArrayPush(ctx context.Context, a, element Value, prepend bool) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if a.Type() == nil || !a.Type().IsArray() || element.Type() != a.Type().Element() {
		return Value{}, fmt.Errorf("invalid array push generation")
	}
	if a.ArrayRank() > 1 {
		return Value{}, failure("22000", "argument must be empty or one-dimensional array")
	}
	dim := ArrayDimension{Lower: 1, Length: 1}
	if a.ArrayRank() != 0 {
		dim = a.ArrayDimension(0)
		next := int64(dim.Lower) + int64(dim.Length)
		if prepend {
			next = int64(dim.Lower) - 1
		}
		if next < math.MinInt32 || next > math.MaxInt32 {
			return Value{}, failure("22003", "integer out of range")
		}
		dim.Length++
	}
	dims := []ArrayDimension{dim}
	n, err := arrayShapeSize(dims)
	if err != nil {
		return Value{}, err
	}
	out := make([]Value, n)
	start := 0
	if prepend {
		out[0] = element
		start = 1
	} else {
		out[n-1] = element
	}
	for i := range a.ArrayLen() {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		out[start+i] = a.ArrayElement(i)
	}
	return OwnedArrayValue(a.Type(), dims, out)
}

func ArrayCat(ctx context.Context, a, b Value) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if a.Type() == nil || !a.Type().IsArray() || a.Type() != b.Type() {
		return Value{}, fmt.Errorf("invalid array concatenation generation")
	}
	if a.IsNull() {
		return b, nil
	}
	if b.IsNull() {
		return a, nil
	}
	x, y := a.ArrayRank(), b.ArrayRank()
	// Only rank-zero arrays are neutral. A rank-one empty ACL is a real
	// shape: retain its bounds when concatenating a nonempty peer.
	if x == 0 {
		return b, nil
	}
	if y == 0 {
		return a, nil
	}
	// Both nonzero-rank empty inputs can only be the checked empty ACL
	// shape. Reuse that immutable payload rather than passing zero extents
	// through the generic positive-extent constructor.
	if a.ArrayLen() == 0 && b.ArrayLen() == 0 {
		return a, nil
	}
	incompatible := func(detail string) (Value, error) {
		e := failure("2202E", "cannot concatenate incompatible arrays").(*sqlstate.Error)
		e.Detail = detail
		return Value{}, e
	}
	if x-y > 1 || y-x > 1 {
		return incompatible(fmt.Sprintf("Arrays of %d and %d dimensions are not compatible for concatenation.", x, y))
	}
	var dims []ArrayDimension
	if x == y {
		for i := 1; i < x; i++ {
			if a.ArrayDimension(i) != b.ArrayDimension(i) {
				return incompatible("Arrays with differing element dimensions are not compatible for concatenation.")
			}
		}
		dims = a.ArrayDimensions()
		n := int64(dims[0].Length) + int64(b.ArrayDimension(0).Length)
		if n > math.MaxInt32 {
			return Value{}, arraySizeFailure()
		}
		dims[0].Length = int32(n)
	} else {
		large, small := a, b
		if y > x {
			large, small = b, a
		}
		for i := range small.ArrayRank() {
			if small.ArrayDimension(i) != large.ArrayDimension(i+1) {
				return incompatible("Arrays with differing dimensions are not compatible for concatenation.")
			}
		}
		dims = large.ArrayDimensions()
		if dims[0].Length == math.MaxInt32 {
			return Value{}, arraySizeFailure()
		}
		dims[0].Length++
	}
	n, err := arrayShapeSize(dims)
	if err != nil {
		return Value{}, err
	}
	out := make([]Value, n)
	for i := range n {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		if i < a.ArrayLen() {
			out[i] = a.ArrayElement(i)
		} else {
			out[i] = b.ArrayElement(i - a.ArrayLen())
		}
	}
	return OwnedArrayValue(a.Type(), dims, out)
}

func ArrayFill(ctx context.Context, result *Descriptor, value, lengths Value, bounds *Value) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if result == nil || !result.IsArray() || result.Element() != value.Type() || lengths.Type() == nil || !lengths.Type().IsArray() || lengths.Type().Element().ID() != Int4 || bounds != nil && bounds.Type() != lengths.Type() {
		return Value{}, fmt.Errorf("invalid array fill generation")
	}
	if lengths.IsNull() || bounds != nil && bounds.IsNull() {
		return Value{}, failure("22004", "dimension array or low bound array cannot be null")
	}
	vectorError := func(detail string) error {
		e := failure("2202E", "wrong number of array subscripts").(*sqlstate.Error)
		e.Detail = detail
		return e
	}
	if lengths.ArrayRank() > 1 {
		return Value{}, vectorError("Dimension array must be one dimensional.")
	}
	// PostgreSQL validates NULL members before dimension count, then validates
	// bounds' rank and members before comparing the two vector lengths.
	n := lengths.ArrayLen()
	for i := range n {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		if lengths.ArrayElement(i).IsNull() {
			return Value{}, failure("22004", "dimension values cannot be null")
		}
	}
	if n > 6 {
		return Value{}, failure("54000", fmt.Sprintf("number of array dimensions (%d) exceeds the maximum allowed (6)", n))
	}
	if bounds != nil {
		if bounds.ArrayRank() > 1 {
			return Value{}, vectorError("Dimension array must be one dimensional.")
		}
		for i := range bounds.ArrayLen() {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			if bounds.ArrayElement(i).IsNull() {
				return Value{}, failure("22004", "dimension values cannot be null")
			}
		}
		if bounds.ArrayLen() != n {
			return Value{}, vectorError("Low bound array has different size than dimensions array.")
		}
	}
	var scratch [6]ArrayDimension
	for i := range n {
		length, err := lengths.ArrayElement(i).Integer()
		if err != nil {
			return Value{}, err
		}
		scratch[i] = ArrayDimension{Lower: 1, Length: int32(length)}
		if bounds != nil {
			lower, err := bounds.ArrayElement(i).Integer()
			if err != nil {
				return Value{}, err
			}
			scratch[i].Lower = int32(lower)
		}
	}
	count, err := arrayShapeSize(scratch[:n])
	if err != nil {
		return Value{}, err
	}
	if count == 0 {
		return OwnedArrayValue(result, nil, nil)
	}
	out := make([]Value, count)
	for i := range out {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		out[i] = value
	}
	return OwnedArrayValue(result, slices.Clone(scratch[:n]), out)
}

func ArrayReverse(ctx context.Context, a Value) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if a.ArrayRank() == 0 || a.ArrayDimension(0).Length < 2 {
		return a, nil
	}
	n := int(a.ArrayDimension(0).Length)
	width := a.ArrayLen() / n
	out := make([]Value, a.ArrayLen())
	for i := range n {
		for j := range width {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			out[i*width+j] = a.ArrayElement((n-1-i)*width + j)
		}
	}
	return OwnedArrayValue(a.Type(), a.ArrayDimensions(), out)
}

func ArrayTrim(ctx context.Context, a Value, count int64) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	n := int64(0)
	if a.ArrayRank() != 0 {
		n = int64(a.ArrayDimension(0).Length)
	}
	if count < 0 || count > n {
		return Value{}, failure("2202E", fmt.Sprintf("number of elements to trim must be between 0 and %d", n))
	}
	if n == count {
		if a.ArrayLen() == 0 {
			return a, nil
		}
		return OwnedArrayValue(a.Type(), nil, nil)
	}
	if count == 0 {
		same := true
		for i := range a.ArrayRank() {
			same = same && a.ArrayDimension(i).Lower == 1
		}
		if same {
			return a, nil
		}
	}
	first := a.ArrayDimension(0)
	return ArraySlice(ctx, a, []ArraySubscriptValue{{HasUpper: true, Upper: int32(int64(first.Lower) + n - count - 1)}})
}

// ArraySortPlan retains generation-owned leaf authority. Evaluation orders
// first-axis blocks without temporary slice Values or comparison buffers.
type ArraySortPlan struct {
	array    *Descriptor
	leaf     Comparison
	ref      CollationRef
	conflict bool
}

func BindArraySort(array *Descriptor, ref CollationRef, conflict bool) (ArraySortPlan, error) {
	if array == nil || !array.IsArray() {
		return ArraySortPlan{}, fmt.Errorf("invalid array sort descriptor")
	}
	// Composite shape-only calls need no comparison provider. Retain the
	// exact element generation, but demand its existing boundary only when
	// evaluation actually has at least two first-axis items to sort.
	if element := array.Element(); element.BaseType().IsComposite() {
		return ArraySortPlan{array: array, leaf: Comparison{left: element, right: element}, ref: ref, conflict: conflict}, nil
	}
	leaf, err := bindArrayComparisonElement(array, ref, conflict)
	if err != nil {
		return ArraySortPlan{}, err
	}
	return ArraySortPlan{array: array, leaf: *leaf, ref: ref, conflict: conflict}, nil
}

func (p ArraySortPlan) Equal(q ArraySortPlan) bool {
	return p.array == q.array && p.ref == q.ref && p.conflict == q.conflict && p.leaf.Equal(q.leaf)
}
func (p ArraySortPlan) InputCollation() (CollationRef, bool) { return p.ref, p.conflict }
func (p ArraySortPlan) SessionDependent() bool               { return p.leaf.SessionDependent() }

func (p ArraySortPlan) Evaluate(ctx context.Context, a Value, descending, nullsFirst bool, input InputEnvironment) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if a.Type() != p.array {
		return Value{}, fmt.Errorf("foreign array sort generation")
	}
	if a.ArrayRank() == 0 || a.ArrayDimension(0).Length < 2 {
		return a, nil
	}
	if element := p.array.Element(); element.BaseType().IsComposite() {
		_, err := bindComparisonTypes(element, element)
		return Value{}, err
	}
	if err := p.leaf.demandOrdering(); err != nil {
		if a.ArrayRank() == 1 {
			if e, ok := err.(*sqlstate.Error); ok {
				copy := *e
				copy.Code = "0A000"
				err = &copy
			}
		}
		return Value{}, err
	}
	s := arrayBlockSorter{
		ctx: ctx, values: slices.Clone(a.array.elements), width: a.ArrayLen() / int(a.ArrayDimension(0).Length),
		leaf: p.leaf, input: input, descending: descending, nullsFirst: nullsFirst, scalar: a.ArrayRank() == 1,
	}
	sort.Sort(&s)
	if s.err != nil {
		return Value{}, s.err
	}
	return OwnedArrayValue(a.Type(), a.ArrayDimensions(), s.values)
}

type arrayBlockSorter struct {
	ctx                            context.Context
	values                         []Value
	width                          int
	leaf                           Comparison
	input                          InputEnvironment
	descending, nullsFirst, scalar bool
	err                            error
}

func (s *arrayBlockSorter) Len() int { return len(s.values) / s.width }
func (s *arrayBlockSorter) Swap(i, j int) {
	if s.err != nil {
		return
	}
	for k := range s.width {
		if s.err = s.ctx.Err(); s.err != nil {
			return
		}
		s.values[i*s.width+k], s.values[j*s.width+k] = s.values[j*s.width+k], s.values[i*s.width+k]
	}
}
func (s *arrayBlockSorter) Less(i, j int) bool {
	if s.err != nil {
		return false
	}
	for k := range s.width {
		if s.err = s.ctx.Err(); s.err != nil {
			return false
		}
		a, b := s.values[i*s.width+k], s.values[j*s.width+k]
		sign := 0
		if a.IsNull() || b.IsNull() {
			if a.IsNull() == b.IsNull() {
				continue
			}
			if s.scalar {
				return a.IsNull() == s.nullsFirst
			}
			if a.IsNull() {
				sign = 1
			} else {
				sign = -1
			}
		} else {
			sign, s.err = s.leaf.Compare(s.ctx, a, b, s.input)
			if s.err != nil {
				return false
			}
		}
		if sign != 0 {
			if s.descending {
				return sign > 0
			}
			return sign < 0
		}
	}
	return false
}
