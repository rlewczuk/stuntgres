package sqltype

import (
	"context"
	"encoding/binary"
	"math/bits"
)

// PostgreSQL REL_18_0 common/hashfn.c. Arithmetic deliberately wraps at 32 bits.
type partitionHashState struct {
	a, b, c uint32
	tail    [12]byte
	n       int
}

func newPartitionHashState(length int, seed uint64) partitionHashState {
	a := uint32(0x9e3779b9) + uint32(length) + 3923095
	s := partitionHashState{a: a, b: a, c: a}
	if seed != 0 {
		s.a += uint32(seed >> 32)
		s.b += uint32(seed)
		s.mix()
	}
	return s
}
func (s *partitionHashState) mix() {
	s.a -= s.c
	s.a ^= bits.RotateLeft32(s.c, 4)
	s.c += s.b
	s.b -= s.a
	s.b ^= bits.RotateLeft32(s.a, 6)
	s.a += s.c
	s.c -= s.b
	s.c ^= bits.RotateLeft32(s.b, 8)
	s.b += s.a
	s.a -= s.c
	s.a ^= bits.RotateLeft32(s.c, 16)
	s.c += s.b
	s.b -= s.a
	s.b ^= bits.RotateLeft32(s.a, 19)
	s.a += s.c
	s.c -= s.b
	s.c ^= bits.RotateLeft32(s.b, 4)
	s.b += s.a
}
func (s *partitionHashState) final() uint64 {
	s.c ^= s.b
	s.c -= bits.RotateLeft32(s.b, 14)
	s.a ^= s.c
	s.a -= bits.RotateLeft32(s.c, 11)
	s.b ^= s.a
	s.b -= bits.RotateLeft32(s.a, 25)
	s.c ^= s.b
	s.c -= bits.RotateLeft32(s.b, 16)
	s.a ^= s.c
	s.a -= bits.RotateLeft32(s.c, 4)
	s.b ^= s.a
	s.b -= bits.RotateLeft32(s.a, 14)
	s.c ^= s.b
	s.c -= bits.RotateLeft32(s.b, 24)
	return uint64(s.b)<<32 | uint64(s.c)
}
func (s *partitionHashState) put(b byte) {
	s.tail[s.n] = b
	s.n++
	if s.n == 12 {
		s.a += binary.LittleEndian.Uint32(s.tail[:4])
		s.b += binary.LittleEndian.Uint32(s.tail[4:8])
		s.c += binary.LittleEndian.Uint32(s.tail[8:])
		s.mix()
		s.n = 0
	}
}
func (s *partitionHashState) finish() uint64 {
	for i := range s.n {
		if i < 4 {
			s.a += uint32(s.tail[i]) << uint(i*8)
		} else if i < 8 {
			s.b += uint32(s.tail[i]) << uint((i-4)*8)
		} else {
			s.c += uint32(s.tail[i]) << uint((i-7)*8)
		}
	}
	return s.final()
}
func partitionHashBytes[T ~string | ~[]byte](ctx context.Context, data T, seed uint64) (uint64, error) {
	s := newPartitionHashState(len(data), seed)
	i := 0
	for ; i+12 <= len(data); i += 12 {
		if i&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		s.a += uint32(data[i]) | uint32(data[i+1])<<8 | uint32(data[i+2])<<16 | uint32(data[i+3])<<24
		s.b += uint32(data[i+4]) | uint32(data[i+5])<<8 | uint32(data[i+6])<<16 | uint32(data[i+7])<<24
		s.c += uint32(data[i+8]) | uint32(data[i+9])<<8 | uint32(data[i+10])<<16 | uint32(data[i+11])<<24
		s.mix()
	}
	for ; i < len(data); i++ {
		s.put(data[i])
	}
	return s.finish(), ctx.Err()
}
func partitionHashUint32(v uint32, seed uint64) uint64 {
	s := newPartitionHashState(4, seed)
	s.a += v
	return s.final()
}
func partitionHashInt64(v int64, seed uint64) uint64 {
	lo, hi := uint32(v), uint32(uint64(v)>>32)
	if v < 0 {
		hi = ^hi
	}
	return partitionHashUint32(lo^hi, seed)
}
