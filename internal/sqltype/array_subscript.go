package sqltype

import (
	"context"
	"fmt"
	"math"
	"slices"
	"stuntgres/internal/sqlstate"
)

// ArraySubscriptValue distinguishes omitted bounds from evaluated SQL NULL.
// In slice mode a non-colon index has already been expanded to [1:index].
type ArraySubscriptValue struct {
	Lower, Upper                             int32
	HasLower, HasUpper, LowerNull, UpperNull bool
}

func ConstructArray(ctx context.Context, d *Descriptor, children []Value, multidimensional bool) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if !multidimensional {
		var dims []ArrayDimension
		if len(children) > 0 {
			dims = []ArrayDimension{{1, int32(len(children))}}
		}
		return OwnedArrayValue(d, dims, children)
	}
	var dims []ArrayDimension
	size := 0
	empty := false
	for _, v := range children {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		if v.ArrayLen() == 0 {
			empty = true
			continue
		}
		if dims == nil {
			dims = v.array.dimensions
		} else if !slices.Equal(dims, v.array.dimensions) {
			return Value{}, failure("2202E", "multidimensional arrays must have array expressions with matching dimensions")
		}
		if size > math.MaxInt32-v.ArrayLen() {
			return Value{}, failure("54000", "array size exceeds the maximum allowed")
		}
		size += v.ArrayLen()
	}
	if size == 0 {
		return OwnedArrayValue(d, nil, nil)
	}
	if empty {
		return Value{}, failure("2202E", "multidimensional arrays must have array expressions with matching dimensions")
	}
	if len(dims) >= 6 {
		return Value{}, failure("54000", "number of array dimensions (7) exceeds the maximum allowed (6)")
	}
	elements := make([]Value, 0, size)
	for _, v := range children {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		elements = append(elements, v.array.elements...)
	}
	shape := make([]ArrayDimension, len(dims)+1)
	shape[0] = ArrayDimension{1, int32(len(children))}
	copy(shape[1:], dims)
	return OwnedArrayValue(d, shape, elements)
}

func arrayOffset(dims []ArrayDimension, indices []ArraySubscriptValue) (int, bool) {
	if len(dims) == 0 || len(dims) != len(indices) {
		return 0, false
	}
	offset := 0
	for i, d := range dims {
		n := indices[i]
		delta := int64(n.Upper) - int64(d.Lower)
		if n.UpperNull || delta < 0 || delta >= int64(d.Length) {
			return 0, false
		}
		offset = offset*int(d.Length) + int(delta)
	}
	return offset, true
}

// ArrayFetch reads the immutable payload directly, without cloning dimensions.
func ArrayFetch(ctx context.Context, v Value, indices []ArraySubscriptValue) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	null := Null(v.Type().BaseType().Element())
	if v.IsNull() || v.array == nil {
		return null, nil
	}
	offset, ok := arrayOffset(v.array.dimensions, indices)
	if !ok {
		return null, nil
	}
	return v.array.elements[offset], nil
}

// PostgreSQL's MaxArraySize uses its eight-byte Datum, not Go's Value size.
const maxArrayItems = 0x3fffffff / 8

func arraySizeFailure() error {
	return failure("54000", "array size exceeds the maximum allowed (134217727)")
}

// Item count and bounds are separate because an empty-original assignment
// checks source sufficiency before construct_md_array checks its lower bounds.
func arrayItemCount(dims []ArrayDimension) (int, error) {
	if len(dims) > 6 {
		return 0, failure("54000", fmt.Sprintf("number of array dimensions (%d) exceeds the maximum allowed (6)", len(dims)))
	}
	if len(dims) == 0 {
		return 0, nil
	}
	size := int64(1)
	for _, d := range dims {
		if d.Length < 0 {
			return 0, arraySizeFailure()
		}
		size *= int64(d.Length)
		if size > math.MaxInt32 {
			return 0, arraySizeFailure()
		}
	}
	if size > maxArrayItems {
		return 0, arraySizeFailure()
	}
	return int(size), nil
}

func arrayDimensionBounds(dims []ArrayDimension) error {
	for _, d := range dims {
		if int64(d.Lower)+int64(d.Length) > math.MaxInt32 {
			return failure("54000", fmt.Sprintf("array lower bound is too large: %d", d.Lower))
		}
	}
	return nil
}

func arrayShapeSize(dims []ArrayDimension) (int, error) {
	size, err := arrayItemCount(dims)
	if err != nil {
		return size, err
	}
	if err := arrayDimensionBounds(dims); err != nil {
		return 0, err
	}
	return size, nil
}

// arrayRectangle copies the selected row-major rectangle to a fresh owned
// payload. Fixed-size coordinate state avoids per-cell allocations.
func arrayRectangle(ctx context.Context, v Value, selected []ArrayDimension, elements []Value) error {
	var coords [6]int32
	for i, d := range selected {
		coords[i] = d.Lower
	}
	for at := range elements {
		if err := ctx.Err(); err != nil {
			return err
		}
		offset := 0
		for i, d := range v.array.dimensions {
			offset = offset*int(d.Length) + int(coords[i]-d.Lower)
		}
		elements[at] = v.array.elements[offset]
		for i := len(selected) - 1; i >= 0; i-- {
			coords[i]++
			if int64(coords[i]) < int64(selected[i].Lower)+int64(selected[i].Length) {
				break
			}
			coords[i] = selected[i].Lower
		}
	}
	return nil
}

func ArraySlice(ctx context.Context, v Value, bounds []ArraySubscriptValue) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	d := v.Type().BaseType()
	for _, b := range bounds {
		if b.LowerNull || b.UpperNull {
			return Null(d), nil
		}
	}
	if v.IsNull() {
		return Null(d), nil
	}
	if v.ArrayLen() == 0 || len(bounds) > len(v.array.dimensions) {
		return OwnedArrayValue(d, nil, nil)
	}
	selected := make([]ArrayDimension, len(v.array.dimensions))
	for i, dim := range v.array.dimensions {
		lower, upper := int64(dim.Lower), int64(dim.Lower)+int64(dim.Length)-1
		if i < len(bounds) {
			b := bounds[i]
			if b.HasLower && int64(b.Lower) > lower {
				lower = int64(b.Lower)
			}
			if b.HasUpper && int64(b.Upper) < upper {
				upper = int64(b.Upper)
			}
		}
		if lower > upper {
			return OwnedArrayValue(d, nil, nil)
		}
		selected[i] = ArrayDimension{int32(lower), int32(upper - lower + 1)}
	}
	size, err := arrayShapeSize(selected)
	if err != nil {
		return Value{}, err
	}
	elements := make([]Value, size)
	if err := arrayRectangle(ctx, v, selected, elements); err != nil {
		return Value{}, err
	}
	for i := range selected {
		selected[i].Lower = 1
	}
	return OwnedArrayValue(d, selected, elements)
}

func assignmentBounds(bounds []ArraySubscriptValue) error {
	for _, b := range bounds {
		if b.LowerNull || b.UpperNull {
			return failure("22004", "array subscript in assignment must not be null")
		}
	}
	return nil
}

func ArraySetElement(ctx context.Context, v Value, bounds []ArraySubscriptValue, rhs Value) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if err := assignmentBounds(bounds); err != nil {
		return Value{}, err
	}
	d := v.Type().BaseType()
	if v.ArrayLen() == 0 {
		dims := make([]ArrayDimension, len(bounds))
		for i, b := range bounds {
			dims[i] = ArrayDimension{b.Upper, 1}
		}
		if _, err := arrayShapeSize(dims); err != nil {
			return Value{}, err
		}
		return OwnedArrayValue(d, dims, []Value{rhs})
	}
	if len(bounds) != len(v.array.dimensions) {
		return Value{}, failure("2202E", "wrong number of array subscripts")
	}
	if offset, ok := arrayOffset(v.array.dimensions, bounds); ok {
		elements := slices.Clone(v.array.elements)
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		elements[offset] = rhs
		return OwnedArrayValue(d, v.array.dimensions, elements)
	}
	if len(bounds) != 1 {
		return Value{}, failure("2202E", "array subscript out of range")
	}
	old := v.array.dimensions[0]
	lower, upper := int64(old.Lower), int64(old.Lower)+int64(old.Length)-1
	index := int64(bounds[0].Upper)
	if index < lower {
		lower = index
	}
	if index > upper {
		upper = index
	}
	if upper-lower+1 > math.MaxInt32 {
		return Value{}, arraySizeFailure()
	}
	dims := []ArrayDimension{{int32(lower), int32(upper - lower + 1)}}
	size, err := arrayShapeSize(dims)
	if err != nil {
		return Value{}, err
	}
	elements := make([]Value, size)
	oldStart := int(int64(old.Lower) - lower)
	replacement := int(index - lower)
	copy(elements[oldStart:], v.array.elements)
	elements[replacement] = rhs
	gapStart, gapEnd := oldStart+len(v.array.elements), replacement
	if replacement < oldStart {
		gapStart, gapEnd = replacement+1, oldStart
	}
	for i := gapStart; i < gapEnd; i++ {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		elements[i] = Null(d.Element())
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	return OwnedArrayValue(d, dims, elements)
}

func ArraySetSlice(ctx context.Context, v Value, bounds []ArraySubscriptValue, rhs Value) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if err := assignmentBounds(bounds); err != nil {
		return Value{}, err
	}
	d := v.Type().BaseType()
	if rhs.IsNull() {
		if v.IsNull() {
			return OwnedArrayValue(d, nil, nil)
		}
		v.typ = d
		return v, nil
	}

	// Empty originals construct explicit spans, unlike nonempty inversion
	// checks. Validate every span before canonicalizing a zero-sized result.
	if v.ArrayLen() == 0 {
		if len(bounds) > 6 {
			return Value{}, failure("54000", fmt.Sprintf("number of array dimensions (%d) exceeds the maximum allowed (6)", len(bounds)))
		}
		var storage [6]ArrayDimension
		dims := storage[:len(bounds)]
		for i, b := range bounds {
			if !b.HasLower || !b.HasUpper {
				err := failure("2202E", "array slice subscript must provide both boundaries")
				err.(*sqlstate.Error).Detail = "When assigning to a slice of an empty array value, slice boundaries must be fully specified."
				return Value{}, err
			}
			delta := int64(b.Upper) - int64(b.Lower)
			if delta < math.MinInt32 || delta >= math.MaxInt32 {
				return Value{}, arraySizeFailure()
			}
			dims[i] = ArrayDimension{b.Lower, int32(delta + 1)}
		}
		required, err := arrayItemCount(dims)
		if err != nil {
			return Value{}, err
		}
		if rhs.ArrayLen() < required {
			return Value{}, failure("2202E", "source array too small")
		}
		if err := arrayDimensionBounds(dims); err != nil {
			return Value{}, err
		}
		if required == 0 {
			return OwnedArrayValue(d, nil, nil)
		}
		shape := make([]ArrayDimension, len(dims))
		copy(shape, dims)
		elements := slices.Clone(rhs.array.elements[:required])
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		return OwnedArrayValue(d, shape, elements)
	}

	rank := len(v.array.dimensions)
	if len(bounds) > rank {
		return Value{}, failure("2202E", "wrong number of array subscripts")
	}
	var storage [6]ArrayDimension
	selected := storage[:rank]
	finalDimension := v.array.dimensions[0]
	for i, old := range v.array.dimensions {
		lower, upper := int64(old.Lower), int64(old.Lower)+int64(old.Length)-1
		if i < len(bounds) {
			b := bounds[i]
			if b.HasLower {
				lower = int64(b.Lower)
			}
			if b.HasUpper {
				upper = int64(b.Upper)
			}
		}
		if upper < lower {
			return Value{}, failure("2202E", "upper bound cannot be less than lower bound")
		}
		if rank > 1 && (lower < int64(old.Lower) || upper >= int64(old.Lower)+int64(old.Length)) {
			return Value{}, failure("2202E", "array subscript out of range")
		}
		length := upper - lower + 1
		if length > math.MaxInt32 {
			return Value{}, arraySizeFailure()
		}
		selected[i] = ArrayDimension{int32(lower), int32(length)}
		if rank == 1 {
			finalLower := min(lower, int64(old.Lower))
			finalUpper := max(upper, int64(old.Lower)+int64(old.Length)-1)
			if finalUpper-finalLower+1 > math.MaxInt32 {
				return Value{}, arraySizeFailure()
			}
			finalDimension = ArrayDimension{int32(finalLower), int32(finalUpper - finalLower + 1)}
		}
	}
	// Only one-dimensional enlargement changes the owned destination shape.
	// Unchanged dimensions are already valid immutable payload metadata.
	enlarged := rank == 1 && finalDimension != v.array.dimensions[0]
	size := v.ArrayLen()
	if enlarged {
		check := [1]ArrayDimension{finalDimension}
		var err error
		size, err = arrayShapeSize(check[:])
		if err != nil {
			return Value{}, err
		}
	} else if size > maxArrayItems {
		return Value{}, arraySizeFailure()
	}
	required, err := arrayItemCount(selected)
	if err != nil {
		return Value{}, err
	}
	if rhs.ArrayLen() < required {
		return Value{}, failure("2202E", "source array too small")
	}
	shape := v.array.dimensions
	if enlarged {
		shape = []ArrayDimension{finalDimension}
	}
	elements := make([]Value, size)
	if rank == 1 {
		oldStart := int(int64(v.array.dimensions[0].Lower) - int64(shape[0].Lower))
		copy(elements[oldStart:], v.array.elements)
		start := int(int64(selected[0].Lower) - int64(shape[0].Lower))
		copy(elements[start:start+required], rhs.array.elements[:required])
		gapStart, gapEnd := oldStart+len(v.array.elements), start
		if start+required < oldStart {
			gapStart, gapEnd = start+required, oldStart
		}
		for i := gapStart; i < gapEnd; i++ {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			elements[i] = Null(d.Element())
		}
	} else {
		copy(elements, v.array.elements)
		var coords [6]int32
		for i, s := range selected {
			coords[i] = s.Lower
		}
		width := int(selected[rank-1].Length)
		for at := 0; at < required; at += width {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			offset := 0
			for i, s := range shape {
				offset = offset*int(s.Length) + int(coords[i]-s.Lower)
			}
			copy(elements[offset:offset+width], rhs.array.elements[at:at+width])
			for i := rank - 2; i >= 0; i-- {
				coords[i]++
				if int64(coords[i]) < int64(selected[i].Lower)+int64(selected[i].Length) {
					break
				}
				coords[i] = selected[i].Lower
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	return OwnedArrayValue(d, shape, elements)
}
