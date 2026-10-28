package util

import (
	"maps"
	"math"
	"math/rand/v2"
	"testing"
)

func TestPersistentMapZeroValue(t *testing.T) {
	var m PersistentMap[string, int]
	if m.Len() != 0 {
		t.Fatal("zero map has entries")
	}
	if _, ok := m.Get("a"); ok {
		t.Fatal("zero map finds a key")
	}
	for range m.All() {
		t.Fatal("zero map yields an entry")
	}
	if got := m.Delete("a"); got != m {
		t.Fatal("deleting from the zero map changed it")
	}
}

func TestPersistentMapSetGetDelete(t *testing.T) {
	var empty PersistentMap[string, int]
	one := empty.Set("a", 1)
	two := one.Set("b", 2)
	replaced := two.Set("a", 10)
	removed := replaced.Delete("b")
	checkModel(t, empty, realHash, map[string]int{}, "a")
	checkModel(t, one, realHash, map[string]int{"a": 1}, "b")
	checkModel(t, two, realHash, map[string]int{"a": 1, "b": 2}, "c")
	checkModel(t, replaced, realHash, map[string]int{"a": 10, "b": 2}, "c")
	checkModel(t, removed, realHash, map[string]int{"a": 10}, "b")
	if got := removed.Delete("missing"); got != removed {
		t.Fatal("deleting an absent key changed the map")
	}
	if got := removed.Delete("a"); got.Len() != 0 || got.root != nil {
		t.Fatal("deleting the last key left a root")
	}
}

func TestPersistentMapNaNKey(t *testing.T) {
	m := PersistentMap[float64, int]{}.Set(math.NaN(), 1).Set(math.NaN(), 2)
	if m.Len() != 2 {
		t.Fatalf("NaN keys are distinct, as in a builtin map: Len %d", m.Len())
	}
	if _, ok := m.Get(math.NaN()); ok {
		t.Fatal("a NaN key was found")
	}
}

// TestPersistentMapMatchesBuiltinMap applies a long random sequence of
// operations to a PersistentMap and a builtin map and compares them often.
// Every tenth version is kept with a copy of its model and rechecked at the
// end, after every later version derived from it.
func TestPersistentMapMatchesBuiltinMap(t *testing.T) {
	type snapshot struct {
		m     PersistentMap[int, int]
		model map[int]int
	}
	rng := rand.New(rand.NewPCG(1, 2))
	const keys, steps = 3000, 40000
	var m PersistentMap[int, int]
	model := map[int]int{}
	var kept []snapshot
	for step := range steps {
		k := rng.IntN(keys)
		if rng.IntN(10) < 6 {
			m = m.Set(k, step)
			model[k] = step
		} else {
			m = m.Delete(k)
			delete(model, k)
		}
		if step%1000 == 0 {
			checkModel(t, m, realHash, model, -1, keys, keys+1)
		}
		if step%10 == 0 {
			kept = append(kept, snapshot{m, maps.Clone(model)})
		}
	}
	checkModel(t, m, realHash, model)
	for _, s := range kept {
		checkModel(t, s.m, realHash, s.model)
	}
}

func TestPersistentMapVersionsAreIndependent(t *testing.T) {
	base := PersistentMap[int, string]{}
	for i := range 500 {
		base = base.Set(i, "base")
	}
	left, right := base, base
	for i := range 250 {
		left = left.Set(i, "left").Delete(i + 250)
		right = right.Set(i+1000, "right")
	}
	baseModel, leftModel, rightModel := map[int]string{}, map[int]string{}, map[int]string{}
	for i := range 500 {
		baseModel[i], rightModel[i] = "base", "base"
	}
	for i := range 250 {
		leftModel[i] = "left"
		rightModel[i+1000] = "right"
	}
	checkModel(t, base, realHash, baseModel)
	checkModel(t, left, realHash, leftModel)
	checkModel(t, right, realHash, rightModel)
}

func TestPersistentMapAllStopsEarly(t *testing.T) {
	m := PersistentMap[int, int]{}
	for i := range 100 {
		m = m.Set(i, i)
	}
	visited := 0
	for range m.All() {
		visited++
		if visited == 7 {
			break
		}
	}
	if visited != 7 {
		t.Fatalf("visited %d entries after break at 7", visited)
	}
}

func TestPersistentMapEvery(t *testing.T) {
	var empty PersistentMap[int, int]
	if !empty.Every(func(int, int) bool {
		t.Fatal("empty traversal called predicate")
		return false
	}) {
		t.Fatal("empty traversal was not vacuously true")
	}
	m := empty
	for key := range 100 {
		m = m.Set(key, key*3)
	}
	retained := m
	seen := make(map[int]int, m.Len())
	if !m.Every(func(key, value int) bool {
		seen[key]++
		return value == key*3
	}) || len(seen) != m.Len() {
		t.Fatal("complete predicate traversal lost entries", seen)
	}
	for key := range 100 {
		if seen[key] != 1 {
			t.Fatal("predicate did not visit each original entry once", key, seen[key])
		}
	}
	visited := 0
	if m.Every(func(int, int) bool {
		visited++
		return false
	}) || visited != 1 {
		t.Fatal("false predicate did not stop immediately", visited)
	}
	if allocations := testing.AllocsPerRun(10, func() {
		if !m.Every(func(key, value int) bool { return value == key*3 }) {
			t.Fatal("unchanged predicate result")
		}
	}); allocations != 0 {
		t.Fatal("direct predicate traversal allocated", allocations)
	}
	// Rebinding the caller's map during traversal cannot mutate the receiver
	// version or omit any of its original entries.
	visited = 0
	if !m.Every(func(key, value int) bool {
		visited++
		m = m.Delete(key)
		return value == key*3
	}) || visited != 100 || m.Len() != 0 || retained.Len() != 100 {
		t.Fatal("predicate traversal did not retain its immutable version")
	}
	for key := range 100 {
		if value, found := retained.Get(key); !found || value != key*3 {
			t.Fatal("predicate traversal mutated retained backing", key, value, found)
		}
	}
}

// TestPersistentMapWriteDuringIteration rewrites, deletes and adds keys of the
// variable being iterated. The iteration walks the version it started from:
// every original key once, with its original value.
func TestPersistentMapWriteDuringIteration(t *testing.T) {
	m := PersistentMap[int, int]{}
	for i := range 200 {
		m = m.Set(i, i)
	}
	seen := map[int]int{}
	for k, v := range m.All() {
		seen[k] = v
		m = m.Set(k, -1).Delete((k+1)%200).Set(k+1000, k)
	}
	if len(seen) != 200 {
		t.Fatalf("visited %d keys, want 200", len(seen))
	}
	for k, v := range seen {
		if v != k {
			t.Fatalf("key %d yielded %d, want its starting value", k, v)
		}
	}
}

func TestPersistentMapStructKeys(t *testing.T) {
	type address struct {
		class, object uint32
		sub           int32
	}
	type name struct {
		class     uint32
		namespace address
		name      string
		signature string
	}
	var byAddress PersistentMap[address, string]
	var byName PersistentMap[name, address]
	addressModel, nameModel := map[address]string{}, map[name]address{}
	for i := range uint32(2000) {
		a := address{1255, 16384 + i, 0}
		n := name{1255, address{2615, 2200, 0}, "f", string(rune('a' + i%26))}
		n.name += string(rune('a' + i/26%26))
		byAddress, addressModel[a] = byAddress.Set(a, n.name), n.name
		byName, nameModel[n] = byName.Set(n, a), a
	}
	checkModel(t, byAddress, realHash, addressModel, address{1, 2, 3})
	checkModel(t, byName, realHash, nameModel, name{name: "missing"})
}
