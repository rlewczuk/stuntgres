package sqltype

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
)

// PartitionHash executes registered PostgreSQL extended support. IndexHash
// shares its native datum primitives with seed zero and a 32-bit result.
type PartitionHash struct {
	usage   Usage
	class   IndexClass
	input   ID
	routine uint32
}

func (p PartitionHash) SameOperation(other PartitionHash) bool {
	for i := range p.class.support {
		p.class.support[i].Revision, p.class.support[i].FamilyRevision, p.class.support[i].RoutineRevision = 0, 0, 0
		other.class.support[i].Revision, other.class.support[i].FamilyRevision, other.class.support[i].RoutineRevision = 0, 0, 0
		p.class.support[i].ClassRevision,other.class.support[i].ClassRevision=0,0
	}
	return p == other
}

func BindPartitionHash(u Usage, c IndexClass) (PartitionHash, error) {
	if err := validateHashClass(u, c); err != nil {
		return PartitionHash{}, err
	}
	if u.Type.BaseType().IsLtree() {
		if !validLtreeHashSupport(u,c) { return PartitionHash{},fmt.Errorf("invalid installed ltree extended hash support") }
		return PartitionHash{usage:u,class:c,input:c.input,routine:c.Support(2).Routine},nil
	}
	if u.Type.BaseType().IsHstore() {
		if !validHstoreHashSupport(u, c) {
			return PartitionHash{}, fmt.Errorf("invalid installed hstore extended hash support")
		}
		return PartitionHash{usage: u, class: c, input: c.input, routine: c.Support(2).Routine}, nil
	}
	s := c.Support(2)
	if s.Provider != BuiltinIndexSupport {
		return PartitionHash{}, fmt.Errorf("unknown extended hash support provider")
	}
	n, ok := NativeIndexRoutineByOID(s.Routine)
	if !ok || n.Kind != IndexExtendedHashRoutine || n.Left != s.Left || n.Right != uint32(Int8) || s.Left != s.Right || s.Member == 0 {
		return PartitionHash{}, fmt.Errorf("partition hash has no registered native support")
	}
	input := ID(n.Left)
	if input == 3500 {
		input = u.Type.BaseType().id
	}
	return PartitionHash{usage: u, class: c, input: input, routine: n.OID}, nil
}
func (p *PartitionHash) Hash(ctx context.Context, v Value, seed uint64) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	v, err := normalizeHashPayload(v, p.usage, p.class.input)
	if err != nil {
		return 0, err
	}
	if v.null {
		return 0, fmt.Errorf("NULL partition hash argument")
	}
	if v.typ.BaseType().IsLtree() { return LtreeHashExtended(ctx,v,seed) }
	if v.typ.BaseType().IsHstore() {
		return HstoreHash(ctx, v, seed)
	}
	if v.typ.BaseType().IsEnum() {
		return partitionHashUint32(uint32(v.integer), seed), ctx.Err()
	}
	var result uint64
	switch p.input {
	case Bool:
		var n uint32
		if v.boolean {
			n = 1
		}
		result = partitionHashUint32(n, seed)
	case Char:
		var n int8
		if len(v.text) > 0 {
			n = int8(v.text[0])
		}
		result = partitionHashUint32(uint32(int32(n)), seed)
	case Int2, Int4:
		result = partitionHashUint32(uint32(v.integer), seed)
	case Oid, Xid, Cid:
		result = partitionHashUint32(v.oid, seed)
	case Int8, Xid8, Time, Timestamp, Timestamptz:
		result = partitionHashInt64(v.integer, seed)
	case Date:
		n := v.integer
		if n == math.MinInt64 {
			n = math.MinInt32
		} else if n == math.MaxInt64 {
			n = math.MaxInt32
		}
		result = partitionHashUint32(uint32(n), seed)
	case Timetz:
		result = partitionHashInt64(v.integer, seed) ^ partitionHashUint32(uint32(v.west), seed)
	case Interval:
		_, lo := intervalSpan(v)
		result = partitionHashInt64(int64(lo), seed)
	case Float4, Float8:
		if v.floating == 0 {
			return seed, ctx.Err()
		}
		n := math.Float64bits(v.floating)
		if math.IsNaN(v.floating) {
			n = 0x7ff8000000000000
		}
		var data [8]byte
		binary.LittleEndian.PutUint64(data[:], n)
		return partitionHashBytes(ctx, data[:], seed)
	case Tid:
		block := uint32(uint64(v.integer) >> 16)
		var data [6]byte
		binary.LittleEndian.PutUint16(data[:2], uint16(block>>16))
		binary.LittleEndian.PutUint16(data[2:4], uint16(block))
		binary.LittleEndian.PutUint16(data[4:], uint16(v.integer))
		return partitionHashBytes(ctx, data[:], seed)
	case Text, VarChar, Name, Bytea:
		return partitionHashBytes(ctx, v.text, seed)
	case BPChar:
		end := len(v.text)
		for end > 0 && v.text[end-1] == ' ' {
			end--
			if end&1023 == 0 {
				if err := ctx.Err(); err != nil {
					return 0, err
				}
			}
		}
		return partitionHashBytes(ctx, v.text[:end], seed)
	case UUID:
		return partitionHashBytes(ctx, v.uuid[:], seed)
	case Numeric:
		return partitionHashNumeric(ctx, v, seed)
	case JSONB:
		if v.jsonb == nil {
			return 0, fmt.Errorf("missing JSONB partition key")
		}
		return partitionHashJSONB(ctx, &v.jsonb.root, seed)
	default:
		return 0, failure("0A000", "partition hash operator class is unsupported")
	}
	return result, ctx.Err()
}

func partitionHashNumeric(ctx context.Context, v Value, seed uint64) (uint64, error) {
	if v.special != numericFinite {
		return seed, ctx.Err()
	}
	// Trim coefficient zeros without allocating or invoking uncancellable normalization.
	first, last := 0, len(v.text)
	for first < last && v.text[first] == '0' {
		first++
		if first&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
	}
	for last > first && v.text[last-1] == '0' {
		last--
		if last&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
	}
	if first == last {
		return seed - 1, ctx.Err()
	}
	point := int64(len(v.text)) + v.exponent
	weight := floorDiv(point-int64(first)-1, 4)
	low := floorDiv(point-int64(last), 4)
	state := newPartitionHashState(int((weight-low+1)*2), seed)
	for group := weight; group >= low; group-- {
		if group&255 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		var digit uint16
		for place := group*4 + 3; place >= group*4; place-- {
			digit *= 10
			index := point - 1 - place
			if index >= int64(first) && index < int64(last) {
				digit += uint16(v.text[index] - '0')
			}
		}
		state.put(byte(digit))
		state.put(byte(digit >> 8))
	}
	return state.finish() ^ uint64(weight), ctx.Err()
}

func partitionHashJSONB(ctx context.Context, root *jsonbNode, seed uint64) (uint64, error) {
	if root.kind == jsonbArray && len(root.items) == 0 || root.kind == jsonbObject && len(root.fields) == 0 {
		return seed, ctx.Err()
	}
	var h uint64
	scalar := func(n *jsonbNode) error {
		var v uint64
		var err error
		switch n.kind {
		case jsonbNull:
			v = seed + 1
		case jsonbString:
			v, err = partitionHashBytes(ctx, n.text, seed)
		case jsonbNumber:
			v, err = partitionHashNumeric(ctx, n.number.value(), seed)
		case jsonbBoolean:
			if seed == 0 {
				v = 4
				if n.boolean {
					v = 2
				}
			} else {
				var b uint32
				if n.boolean {
					b = 1
				}
				v = partitionHashUint32(b, seed)
			}
		}
		h = ((h << 1) & 0xfffffffefffffffe) | ((h >> 31) & 0x100000001)
		h ^= v
		return err
	}
	if root.kind != jsonbArray && root.kind != jsonbObject {
		h = 0x4000000040000000
		if err := scalar(root); err != nil {
			return 0, err
		}
		return h, ctx.Err()
	}
	var local [32]jsonbWalkFrame
	stack := local[:0]
	n := root
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		switch n.kind {
		case jsonbArray:
			h ^= 0x4000000040000000
			if len(n.items) > 0 {
				stack = append(stack, jsonbWalkFrame{node: n, index: 1})
				n = &n.items[0]
				continue
			}
		case jsonbObject:
			h ^= 0x2000000020000000
			if len(n.fields) > 0 {
				key := jsonbNode{kind: jsonbString, text: n.fields[0].key}
				if err := scalar(&key); err != nil {
					return 0, err
				}
				stack = append(stack, jsonbWalkFrame{node: n, index: 1})
				n = &n.fields[0].value
				continue
			}
		default:
			if err := scalar(n); err != nil {
				return 0, err
			}
		}
		for {
			if len(stack) == 0 {
				return h, ctx.Err()
			}
			f := &stack[len(stack)-1]
			i := f.index
			if f.node.kind == jsonbArray {
				if i < len(f.node.items) {
					f.index++
					n = &f.node.items[i]
					break
				}
			} else if i < len(f.node.fields) {
				key := jsonbNode{kind: jsonbString, text: f.node.fields[i].key}
				if err := scalar(&key); err != nil {
					return 0, err
				}
				f.index++
				n = &f.node.fields[i].value
				break
			}
			stack = stack[:len(stack)-1]
		}
	}
}
