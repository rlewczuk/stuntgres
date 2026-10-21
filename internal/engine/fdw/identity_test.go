package fdw

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
		set := IdentitySet{heads: make(map[uint64]int)}
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
		set := IdentitySet{heads: make(map[uint64]int)}
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
		var set IdentitySet
		const count = 8192
		for i := range count {
			identity := catalog.ForeignRowIdentity{int64(i), []byte{byte(i), byte(i >> 8)}, true}
			if !set.Add(identity) {
				t.Fatal("distinct typed identity suppressed", i)
			}
		}
		for i := range count {
			identity := catalog.ForeignRowIdentity{int64(i), []byte{byte(i), byte(i >> 8)}, true}
			if set.Add(identity) {
				t.Fatal("equal typed identity hash diverged", i)
			}
		}
		if len(set.entries) != count {
			t.Fatal("typed membership grew on repeats")
		}
		original := []byte("borrowed")
		owned := exec.CopyForeignCells(catalog.ForeignRowIdentity{original})
		if !set.Add(owned) {
			t.Fatal("owned identity was not distinct")
		}
		original[0] = 'X'
		if set.Add(catalog.ForeignRowIdentity{[]byte("borrowed")}) {
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
			if !identityEqual(pair[0], pair[1]) || identityHash(seed, pair[0]) != identityHash(seed, pair[1]) {
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
		if IdentityComparable(value) {
			t.Fatal("runtime-unhashable identity admitted", value)
		}
	}
	value := fdwUnitDecimalToken{coefficient: 7, metadata: "stable"}
	equal := fdwUnitDecimalToken{coefficient: 7, metadata: "stable"}
	pointer := &value
	otherPointer := &equal
	pointerWithSlice := &fdwUnitDecimalToken{coefficient: 7, metadata: []byte{1}}
	var set IdentitySet
	seed := maphash.MakeSeed()
	for _, tc := range []struct {
		cell  driver.Value
		fresh bool
	}{
		{value, true}, {equal, false},
		{pointer, true}, {pointer, false}, {otherPointer, true},
		{pointerWithSlice, true}, {pointerWithSlice, false},
	} {
		if !driver.IsValue(tc.cell) || !IdentityComparable(tc.cell) {
			t.Fatal("comparable driver extension rejected", tc.cell)
		}
		identity := catalog.ForeignRowIdentity{tc.cell}
		if !identityEqual(identity, identity) || identityHash(seed, identity) != identityHash(seed, identity) {
			t.Fatal("comparable extension lost exact equality/hash", tc.cell)
		}
		if set.Add(identity) != tc.fresh {
			t.Fatal("comparable extension identity was normalized", tc.cell, tc.fresh)
		}
	}
}

func TestFDWModifyIdentity(t *testing.T) {
	t.Run("KeyedMembership", fdwIdentityMembership)
	bytes := []byte("key")
	identity := catalog.ForeignRowIdentity{int64(7), bytes, nil}
	owned := exec.CopyForeignCells(identity)
	bytes[0] = 'X'
	if !identityEqual(owned, catalog.ForeignRowIdentity{int64(7), []byte("key"), nil}) || identityEqual(identity, owned) {
		t.Fatal("identity ownership/equality")
	}
	if identityEqual(owned, catalog.ForeignRowIdentity{int64(7)}) || identityEqual(owned, catalog.ForeignRowIdentity{int64(8), []byte("key"), nil}) {
		t.Fatal("identity aliases a different target")
	}
	clock := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	for _, tc := range []struct {
		name  string
		a, b  catalog.ForeignRowIdentity
		equal bool
	}{
		{"empty", nil, catalog.ForeignRowIdentity{}, true},
		{"nil-cell", catalog.ForeignRowIdentity{nil}, catalog.ForeignRowIdentity{nil}, true},
		{"nil-byte-vs-nil", catalog.ForeignRowIdentity{[]byte(nil)}, catalog.ForeignRowIdentity{nil}, false},
		{"nil-vs-empty-bytes", catalog.ForeignRowIdentity{[]byte(nil)}, catalog.ForeignRowIdentity{[]byte{}}, true},
		{"owned-bytes", catalog.ForeignRowIdentity{[]byte("key")}, catalog.ForeignRowIdentity{[]byte("key")}, true},
		{"bytes-vs-text", catalog.ForeignRowIdentity{[]byte("key")}, catalog.ForeignRowIdentity{"key"}, false},
		{"cell-boundaries", catalog.ForeignRowIdentity{[]byte("ab"), []byte("c")}, catalog.ForeignRowIdentity{[]byte("a"), []byte("bc")}, false},
		{"numeric-types", catalog.ForeignRowIdentity{int64(1)}, catalog.ForeignRowIdentity{float64(1)}, false},
		{"signed-zero", catalog.ForeignRowIdentity{float64(0)}, catalog.ForeignRowIdentity{math.Copysign(0, -1)}, true},
		{"nan", catalog.ForeignRowIdentity{math.NaN()}, catalog.ForeignRowIdentity{math.NaN()}, false},
		{"boolean", catalog.ForeignRowIdentity{true}, catalog.ForeignRowIdentity{true}, true},
		{"strict-time-equal", catalog.ForeignRowIdentity{clock}, catalog.ForeignRowIdentity{clock}, true},
		{"strict-time-location", catalog.ForeignRowIdentity{clock}, catalog.ForeignRowIdentity{clock.In(time.FixedZone("other", 0))}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if identityEqual(tc.a, tc.b) != tc.equal || identityEqual(tc.b, tc.a) != tc.equal {
				t.Fatal("typed opaque identity equality changed", tc.a, tc.b, tc.equal)
			}
		})
	}
}
