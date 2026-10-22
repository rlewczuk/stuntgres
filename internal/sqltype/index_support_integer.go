package sqltype

import (
	"context"
	"fmt"
)

func nativeInt4Compare(a, b int64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

// IntegerIndexSupport is the scalar wrapper over the same native primitives
// used by registered integer index and partition support operations.
func IntegerIndexSupport(ctx context.Context, oid uint32, result *Descriptor, a, b Value) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if a.typ == nil || a.typ.id != Int4 {
		return Value{}, fmt.Errorf("invalid integer support argument")
	}
	if a.null || (oid != 450 && b.null) {
		return Null(result), nil
	}
	switch oid {
	case 351:
		if b.typ == nil || b.typ.id != Int4 || result.id != Int4 {
			return Value{}, fmt.Errorf("invalid integer comparator signature")
		}
		return Integer(result, int64(nativeInt4Compare(a.integer, b.integer)))
	case 450:
		if result.id != Int4 {
			return Value{}, fmt.Errorf("invalid integer hash result")
		}
		return Integer(result, int64(int32(partitionHashUint32(uint32(a.integer), 0))))
	case 425:
		if b.typ == nil || b.typ.id != Int8 || result.id != Int8 {
			return Value{}, fmt.Errorf("invalid extended hash signature")
		}
		return Integer(result, int64(partitionHashUint32(uint32(a.integer), uint64(b.integer))))
	}
	return Value{}, fmt.Errorf("unsupported integer support routine")
}
