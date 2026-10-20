package engine

import (
	"database/sql/driver"
	"hash/maphash"
	"math"
	"testing"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
)

func fdwIdentityMembership(t *testing.T) {
	t.Run("DriverExtensions", fdwIdentityDriverExtensions)
	t.Run("DistinctBucketWorkBound", func(t *testing.T) {
		const count = 50000
		set := foreignIdentitySet{heads: make(map[uint64]int)}
		for i := range count {
			if !set.addHashed(catalog.ForeignRowIdentity{int64(i)}, uint64(i)) {
				t.Fatal("lost distinct identity", i)
			}
		}
		if len(set.heads) != count || len(set.entries) != count {
			t.Fatal("distinct membership cardinality", len(set.heads), len(set.entries))
		}
		for i := range count {
			head := set.heads[uint64(i)]
			// A distinct hash has exactly one collision candidate. Lookup cannot
			// traverse any of the other 49,999 entries; no clock/counter oracle.
			if head == 0 || set.entries[head-1].next != 0 {
				t.Fatal("distinct bucket acquired unrelated lookup work", i)
			}
			if set.addHashed(catalog.ForeignRowIdentity{int64(i)}, uint64(i)) {
				t.Fatal("repeated identity inserted", i)
			}
		}
		if len(set.entries) != count {
			t.Fatal("repeats grew membership")
		}
	})
	t.Run("ForcedCollisions", func(t *testing.T) {
		set := foreignIdentitySet{heads: make(map[uint64]int)}
		clock := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
		for _, tc := range []struct {
			identity catalog.ForeignRowIdentity
			fresh    bool
		}{
			{catalog.ForeignRowIdentity{nil}, true},
			{catalog.ForeignRowIdentity{[]byte(nil)}, true},
			{catalog.ForeignRowIdentity{[]byte{}}, false},
			{catalog.ForeignRowIdentity{[]byte("key")}, true},
			{catalog.ForeignRowIdentity{[]byte("key")}, false},
			{catalog.ForeignRowIdentity{"key"}, true},
			{catalog.ForeignRowIdentity{int64(1)}, true},
			{catalog.ForeignRowIdentity{float64(1)}, true},
			{catalog.ForeignRowIdentity{float64(0)}, true},
			{catalog.ForeignRowIdentity{math.Copysign(0, -1)}, false},
			{catalog.ForeignRowIdentity{clock}, true},
			{catalog.ForeignRowIdentity{clock}, false},
			{catalog.ForeignRowIdentity{clock.In(time.FixedZone("other", 0))}, true},
			{catalog.ForeignRowIdentity{math.NaN()}, true},
			{catalog.ForeignRowIdentity{math.NaN()}, true},
		} {
			if set.addHashed(tc.identity, 7) != tc.fresh {
				t.Fatal("hash collision changed exact identity equality", tc.identity, tc.fresh)
			}
		}
	})
	t.Run("TypedHashMembership", func(t *testing.T) {
		var set foreignIdentitySet
		const count = 8192
		for i := range count {
			identity := catalog.ForeignRowIdentity{int64(i), []byte{byte(i), byte(i >> 8)}, true}
			if !set.add(identity) {
				t.Fatal("distinct typed identity suppressed", i)
			}
		}
		for i := range count {
			identity := catalog.ForeignRowIdentity{int64(i), []byte{byte(i), byte(i >> 8)}, true}
			if set.add(identity) {
				t.Fatal("equal typed identity hash diverged", i)
			}
		}
		if len(set.entries) != count {
			t.Fatal("typed membership grew on repeats")
		}
		original := []byte("borrowed")
		owned := exec.CopyForeignCells(catalog.ForeignRowIdentity{original})
		if !set.add(owned) {
			t.Fatal("owned identity was not distinct")
		}
		original[0] = 'X'
		if set.add(catalog.ForeignRowIdentity{[]byte("borrowed")}) {
			t.Fatal("membership lost already-owned bytes")
		}
	})
	t.Run("EqualHashes", func(t *testing.T) {
		seed := maphash.MakeSeed()
		clock := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
		for _, pair := range [][2]catalog.ForeignRowIdentity{
			{nil, {}},
			{{nil}, {nil}},
			{{[]byte(nil)}, {[]byte{}}},
			{{[]byte("key"), int64(7), true}, {[]byte("key"), int64(7), true}},
			{{float64(0)}, {math.Copysign(0, -1)}},
			{{clock}, {clock}},
		} {
			if !foreignIdentityEqual(pair[0], pair[1]) || foreignIdentityHash(seed, pair[0]) != foreignIdentityHash(seed, pair[1]) {
				t.Fatal("equal identities do not share a hash", pair)
			}
		}
	})
}

type fdwUnitDecimalSlice []byte

func (d fdwUnitDecimalSlice) Decompose([]byte) (byte, bool, []byte, int32) {
	return 0, false, d, 0
}

type fdwUnitDecimalParts struct{ coefficient []byte }

func (d fdwUnitDecimalParts) Decompose([]byte) (byte, bool, []byte, int32) {
	return 0, false, d.coefficient, 0
}

type fdwUnitDecimalToken struct {
	coefficient byte
	metadata    any
}

func (d fdwUnitDecimalToken) Decompose(buf []byte) (byte, bool, []byte, int32) {
	return 0, false, append(buf[:0], d.coefficient), 0
}

func fdwIdentityDriverExtensions(t *testing.T) {
	for _, value := range []driver.Value{
		fdwUnitDecimalSlice{7},
		fdwUnitDecimalParts{coefficient: []byte{7}},
		fdwUnitDecimalToken{coefficient: 7, metadata: []byte{1}},
	} {
		if !driver.IsValue(value) {
			t.Fatal("extension witness is not driver-valid", value)
		}
		if foreignIdentityComparable(value) {
			t.Fatal("runtime-unhashable identity admitted", value)
		}
	}
	value := fdwUnitDecimalToken{coefficient: 7, metadata: "stable"}
	equal := fdwUnitDecimalToken{coefficient: 7, metadata: "stable"}
	pointer := &value
	otherPointer := &equal
	pointerWithSlice := &fdwUnitDecimalToken{coefficient: 7, metadata: []byte{1}}
	var set foreignIdentitySet
	seed := maphash.MakeSeed()
	for _, tc := range []struct {
		cell  driver.Value
		fresh bool
	}{
		{value, true}, {equal, false},
		{pointer, true}, {pointer, false}, {otherPointer, true},
		{pointerWithSlice, true}, {pointerWithSlice, false},
	} {
		if !driver.IsValue(tc.cell) || !foreignIdentityComparable(tc.cell) {
			t.Fatal("comparable driver extension rejected", tc.cell)
		}
		identity := catalog.ForeignRowIdentity{tc.cell}
		if !foreignIdentityEqual(identity, identity) || foreignIdentityHash(seed, identity) != foreignIdentityHash(seed, identity) {
			t.Fatal("comparable extension lost exact equality/hash", tc.cell)
		}
		if set.add(identity) != tc.fresh {
			t.Fatal("comparable extension identity was normalized", tc.cell, tc.fresh)
		}
	}
}
