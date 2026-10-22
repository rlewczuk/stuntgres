package sqltype

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
)

type binarySlice struct {
	data []byte
	at   int
}

func (r *binarySlice) take(n int) ([]byte, error) {
	if n < 0 || n > len(r.data)-r.at {
		return nil, binaryShort()
	}
	p := r.data[r.at : r.at+n]
	r.at += n
	return p, nil
}
func (r *binarySlice) int32() (int32, error) {
	p, err := r.take(4)
	if err != nil {
		return 0, err
	}
	return int32(binary.BigEndian.Uint32(p)), nil
}
func (r *binarySlice) field() (p []byte, isNull bool, err error) {
	n, err := r.int32()
	if err != nil {
		return nil, false, err
	}
	if n == -1 {
		return nil, true, nil
	}
	if n < -1 || int64(n) > int64(len(r.data)-r.at) {
		return nil, false, failure("22P03", "insufficient data left in message")
	}
	p, err = r.take(int(n))
	return p, false, err
}
func (d *Descriptor) binaryArray(ctx context.Context, p []byte, typmod int32, env InputEnvironment) (Value, int, error) {
	r := binarySlice{data: p}
	rank, err := r.int32()
	if err != nil {
		return Value{}, 0, err
	}
	if rank < 0 {
		return Value{}, 0, failure("22P03", fmt.Sprintf("invalid number of dimensions: %d", rank))
	}
	if rank > 6 {
		return Value{}, 0, failure("54000", fmt.Sprintf("number of array dimensions (%d) exceeds the maximum allowed (6)", rank))
	}
	flags, err := r.int32()
	if err != nil {
		return Value{}, 0, err
	}
	if flags != 0 && flags != 1 {
		return Value{}, 0, failure("22P03", "invalid array flags")
	}
	oid, err := r.int32()
	if err != nil {
		return Value{}, 0, err
	}
	if err = binaryOIDMismatch(d.element, uint32(oid), 0); err != nil {
		return Value{}, 0, err
	}
	var shape [6]ArrayDimension
	count := int64(1)
	for i := range int(rank) {
		length, err := r.int32()
		if err != nil {
			return Value{}, 0, err
		}
		lower, err := r.int32()
		if err != nil {
			return Value{}, 0, err
		}
		if length < 0 || int64(length) > 134217727 || length != 0 && count > 134217727/int64(length) {
			return Value{}, 0, failure("54000", "array size exceeds the maximum allowed (134217727)")
		}
		if int64(lower)+int64(length)-1 >= math.MaxInt32 {
			return Value{}, 0, failure("54000", fmt.Sprintf("array lower bound is too large: %d", lower))
		}
		shape[i] = ArrayDimension{Lower: lower, Length: length}
		count *= int64(length)
	}
	if rank == 0 {
		count = 0
	}
	// Cardinality is untrusted: grow only for fields actually received.
	var fields []Value
	for i := range int(count) {
		if err := ctx.Err(); err != nil {
			return Value{}, 0, err
		}
		data, isNull, err := r.field()
		if err != nil {
			return Value{}, 0, err
		}
		if isNull {
			fields = append(fields, Null(d.element))
			if err := d.element.CheckDomainInput(ctx, fields[len(fields)-1], env); err != nil {
				return Value{}, 0, err
			}
			continue
		}
		v, n, err := d.element.binaryReceive(ctx, data, typmod, env)
		if err != nil {
			return Value{}, 0, err
		}
		if n != len(data) {
			return Value{}, 0, failure("22P03", fmt.Sprintf("improper binary format in array element %d", i+1))
		}
		fields = append(fields, v)
	}
	var dimensions []ArrayDimension
	if count != 0 {
		dimensions = make([]ArrayDimension, int(rank))
		copy(dimensions, shape[:rank])
	}
	v, err := OwnedArrayValue(d, dimensions, fields)
	return v, r.at, err
}
func (d *Descriptor) binaryComposite(ctx context.Context, p []byte, env InputEnvironment) (Value, int, error) {
	r := binarySlice{data: p}
	count, err := r.int32()
	if err != nil {
		return Value{}, 0, err
	}
	if int(count) != d.CompositeLen() {
		return Value{}, 0, failure("42804", fmt.Sprintf("wrong number of columns: %d, expected %d", count, d.CompositeLen()))
	}
	fields := make([]Value, int(count))
	for i, member := range d.composite.members {
		oid, err := r.int32()
		if err != nil {
			return Value{}, 0, err
		}
		if err = binaryOIDMismatch(member.Usage.Type, uint32(oid), i+1); err != nil {
			return Value{}, 0, err
		}
		data, isNull, err := r.field()
		if err != nil {
			return Value{}, 0, err
		}
		fields[i] = Null(member.Usage.Type)
		if isNull {
			if err := member.Usage.Type.CheckDomainInput(ctx, fields[i], env); err != nil {
				return Value{}, 0, err
			}
			continue
		}
		v, n, err := member.Usage.Type.binaryReceive(ctx, data, member.Usage.Typmod, env)
		if err != nil {
			return Value{}, 0, err
		}
		if n != len(data) {
			return Value{}, 0, failure("22P03", fmt.Sprintf("improper binary format in record column %d", i+1))
		}
		fields[i] = v
	}
	v, err := OwnedCompositeValue(d, fields)
	return v, r.at, err
}
func appendBinaryField(ctx context.Context, dst []byte, v Value, env OutputEnvironment) ([]byte, error) {
	if v.null {
		return binary32(dst, math.MaxUint32), nil
	}
	at := len(dst)
	dst = binary32(dst, 0)
	var err error
	dst, err = v.typ.AppendBinaryWithEnvironment(ctx, dst, v, env)
	if err != nil {
		return dst, err
	}
	length := len(dst) - at - 4
	if length > math.MaxInt32 {
		return dst, failure("54000", "binary value is too large")
	}
	binary.BigEndian.PutUint32(dst[at:], uint32(length))
	return dst, nil
}
func (d *Descriptor) appendBinaryArray(ctx context.Context, dst []byte, v Value, env OutputEnvironment) ([]byte, error) {
	if v.array == nil {
		return dst, fmt.Errorf("missing array payload")
	}
	flags := uint32(0)
	for _, field := range v.array.elements {
		if field.null {
			flags = 1
			break
		}
	}
	dst = binary32(binary32(binary32(dst, uint32(len(v.array.dimensions))), flags), uint32(d.element.id))
	for _, dim := range v.array.dimensions {
		dst = binary32(binary32(dst, uint32(dim.Length)), uint32(dim.Lower))
	}
	for _, field := range v.array.elements {
		if field.typ != d.element {
			return dst, fmt.Errorf("foreign array element generation")
		}
		var err error
		dst, err = appendBinaryField(ctx, dst, field, env)
		if err != nil {
			return dst, err
		}
	}
	return dst, nil
}
func (d *Descriptor) appendBinaryComposite(ctx context.Context, dst []byte, v Value, env OutputEnvironment) ([]byte, error) {
	if !v.validCompositePayload() {
		return dst, fmt.Errorf("invalid composite payload")
	}
	dst = binary32(dst, uint32(len(v.composite.fields)))
	for i, field := range v.composite.fields {
		dst = binary32(dst, uint32(d.composite.members[i].Usage.Type.id))
		var err error
		dst, err = appendBinaryField(ctx, dst, field, env)
		if err != nil {
			return dst, err
		}
	}
	return dst, nil
}
func (d *Descriptor) binaryRange(ctx context.Context, p []byte, env InputEnvironment) (Value, int, error) {
	if len(p) == 0 {
		return Value{}, 0, binaryByteShort()
	}
	r := binarySlice{data: p, at: 1}
	datum := rangeDatum{flags: p[0] & 31}
	if datum.flags&rangeEmpty != 0 {
		datum = rangeDatum{flags: rangeEmpty}
	} else {
		if datum.flags&rangeLowerUnbounded != 0 {
			datum.flags &^= rangeLowerInclusive
		}
		if datum.flags&rangeUpperUnbounded != 0 {
			datum.flags &^= rangeUpperInclusive
		}
		for _, side := range []bool{true, false} {
			flag := rangeUpperUnbounded
			if side {
				flag = rangeLowerUnbounded
			}
			if datum.flags&flag != 0 {
				continue
			}
			length, err := r.int32()
			if err != nil {
				return Value{}, 0, err
			}
			data, err := r.take(int(length))
			if err != nil {
				return Value{}, 0, err
			}
			value, _, err := d.rangeSubtype.binaryReceive(ctx, data, -1, env)
			if err != nil {
				return Value{}, 0, err
			}
			bound := rangeBound{magnitude: value.integer}
			if d.rangeSubtype.id == Numeric {
				bound = rangeBound{value.text, value.exponent, value.scale, value.negative, value.special}
			}
			if side {
				datum.lower = bound
			} else {
				datum.upper = bound
			}
		}
		var err error
		datum, err = d.canonicalRange(datum, false)
		if err != nil {
			return Value{}, 0, err
		}
	}
	if r.at != len(p) {
		return Value{}, 0, failure("08P01", "invalid message format")
	}
	return Value{typ: d, rangeDatum: datum}, r.at, nil
}
func (d *Descriptor) appendBinaryRange(ctx context.Context, dst []byte, x rangeDatum, env OutputEnvironment) ([]byte, error) {
	dst = append(dst, x.flags)
	if x.flags == rangeEmpty {
		return dst, nil
	}
	for _, side := range []bool{true, false} {
		flag, bound := rangeUpperUnbounded, x.upper
		if side {
			flag, bound = rangeLowerUnbounded, x.lower
		}
		if x.flags&flag != 0 {
			continue
		}
		v := Value{typ: d.rangeSubtype, integer: bound.magnitude}
		if d.rangeSubtype.id == Numeric {
			v = bound.numericValue(d.rangeSubtype)
		}
		var err error
		dst, err = appendBinaryField(ctx, dst, v, env)
		if err != nil {
			return dst, err
		}
	}
	return dst, nil
}
func (d *Descriptor) binaryMultirange(ctx context.Context, p []byte, env InputEnvironment) (Value, int, error) {
	r := binarySlice{data: p}
	count, err := r.int32()
	if err != nil {
		return Value{}, 0, err
	}
	size := uint64(uint32(count)) * 8
	if size > 1073741823 {
		return Value{}, 0, failure("XX000", fmt.Sprintf("invalid memory alloc request size %d", size))
	}
	var members []rangeDatum
	for range int(count) {
		length, err := r.int32()
		if err != nil {
			return Value{}, 0, err
		}
		data, err := r.take(int(length))
		if err != nil {
			return Value{}, 0, err
		}
		v, _, err := d.multirangeRange.binaryRange(ctx, data, env)
		if err != nil {
			return Value{}, 0, err
		}
		if v.rangeDatum.flags != rangeEmpty {
			members = append(members, v.rangeDatum)
		}
	}
	if r.at != len(p) {
		return Value{}, 0, failure("08P01", "invalid message format")
	}
	members, err = d.multirangeRange.normalizeRangeMembers(ctx, members)
	if err != nil {
		return Value{}, 0, err
	}
	return Value{typ: d, multirange: &multirangePayload{members}}, r.at, nil
}
func (d *Descriptor) appendBinaryMultirange(ctx context.Context, dst []byte, v Value, env OutputEnvironment) ([]byte, error) {
	if !v.validMultirange() {
		return dst, fmt.Errorf("invalid multirange payload")
	}
	dst = binary32(dst, uint32(len(v.multirange.members)))
	for _, member := range v.multirange.members {
		start := len(dst)
		dst = binary32(dst, 0)
		var err error
		dst, err = d.multirangeRange.appendBinaryRange(ctx, dst, member, env)
		if err != nil {
			return dst, err
		}
		binary.BigEndian.PutUint32(dst[start:], uint32(len(dst)-start-4))
	}
	return dst, nil
}
