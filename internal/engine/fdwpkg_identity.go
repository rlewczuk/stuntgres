package engine

import (
	"bytes"
	"database/sql/driver"
	"encoding/binary"
	"hash/maphash"
	"reflect"
	"time"

	"stuntgres/internal/engine/catalog"
)

// Target scans already own these validated identity vectors and byte cells.
// Membership borrows them until command completion, without another tuple copy.
// Bucket links are one-based so a missing map entry is an empty chain.
type foreignIdentitySet struct {
	seed    maphash.Seed
	heads   map[uint64]int
	entries []foreignIdentityEntry
}

type foreignIdentityEntry struct {
	identity catalog.ForeignRowIdentity
	next     int
}

func (s *foreignIdentitySet) add(identity catalog.ForeignRowIdentity) bool {
	if s.heads == nil {
		s.seed = maphash.MakeSeed()
		s.heads = make(map[uint64]int)
	}
	return s.addHashed(identity, foreignIdentityHash(s.seed, identity))
}

// Keeping hash selection separate permits deterministic collision and bucket
// work-bound tests without counters or alternate equality in production.
func (s *foreignIdentitySet) addHashed(identity catalog.ForeignRowIdentity, hash uint64) bool {
	head := s.heads[hash]
	for index := head; index != 0; index = s.entries[index-1].next {
		if foreignIdentityEqual(s.entries[index-1].identity, identity) {
			return false
		}
	}
	s.entries = append(s.entries, foreignIdentityEntry{identity: identity, next: head})
	s.heads[hash] = len(s.entries)
	return true
}

// Called after driver.IsValue. Decimal extension values may contain dynamic
// noncomparable fields; inspect the actual value, not only its static type.
func foreignIdentityComparable(cell driver.Value) bool {
	switch cell.(type) {
	case nil, []byte, bool, float64, int64, string, time.Time:
		return true
	default:
		return reflect.ValueOf(cell).Comparable()
	}
}

func foreignIdentityHash(seed maphash.Seed, identity catalog.ForeignRowIdentity) uint64 {
	var hash maphash.Hash
	hash.SetSeed(seed)
	var word [8]byte
	binary.LittleEndian.PutUint64(word[:], uint64(len(identity)))
	hash.Write(word[:])
	for _, cell := range identity {
		if bytes, ok := cell.([]byte); ok {
			hash.WriteByte(1)
			binary.LittleEndian.PutUint64(word[:], uint64(len(bytes)))
			hash.Write(word[:])
			hash.Write(bytes)
		} else {
			hash.WriteByte(0)
			// Comparable preserves typed Go ==, including signed floating zero
			// and strict time.Time equality. Validated non-byte cells are scalar.
			binary.LittleEndian.PutUint64(word[:], maphash.Comparable(seed, cell))
			hash.Write(word[:])
		}
	}
	return hash.Sum64()
}

func foreignIdentityEqual(a, b catalog.ForeignRowIdentity) bool {
	if len(a) != len(b) {
		return false
	}
	for i, cell := range a {
		if x, ok := cell.([]byte); ok {
			y, ok := b[i].([]byte)
			if !ok || !bytes.Equal(x, y) {
				return false
			}
		} else if cell != b[i] {
			return false
		}
	}
	return true
}
