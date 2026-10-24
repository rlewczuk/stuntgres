package sqltype

import "slices"

// ArrayForeachSlice returns a contiguous trailing-rank slice, preserving bounds.
// Like ArrayElement, this read-only accessor requires caller-admitted indices:
// 0 < rank <= ArrayRank(), size is the product of the last rank dimensions,
// and offset is an aligned flat element offset with offset+size <= ArrayLen().
// Proper subslices own their buffers; the full-rank immutable value is shared.
func (v Value) ArrayForeachSlice(rank, offset, size int) Value {
	if rank == len(v.array.dimensions) {
		return v
	}
	return Value{typ: v.typ, array: &arrayPayload{
		dimensions: slices.Clone(v.array.dimensions[len(v.array.dimensions)-rank:]),
		elements:   slices.Clone(v.array.elements[offset : offset+size]),
	}}
}
