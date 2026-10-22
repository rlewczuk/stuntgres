package util

import (
	"fmt"
	"maps"
	"testing"
)

// Benchmarks compare PersistentMap with a builtin map at catalog scale, using
// a key shaped like a catalog object address. They are opt-in: run them with
// go test -run '^$' -bench PersistentMap ./internal/util.

type benchAddress struct {
	class, object uint32
	sub           int32
}

func benchKeys(n int) []benchAddress {
	keys := make([]benchAddress, n)
	for i := range keys {
		keys[i] = benchAddress{1255 + uint32(i%7), 100 + uint32(i), 0}
	}
	return keys
}

func benchMaps(n int) ([]benchAddress, map[benchAddress]*int, PersistentMap[benchAddress, *int]) {
	keys := benchKeys(n)
	builtin := make(map[benchAddress]*int, n)
	for i := range keys {
		builtin[keys[i]] = new(int)
	}
	return keys, builtin, PersistentMapFrom(maps.All(builtin))
}

var benchSink int

func BenchmarkPersistentMapGet(b *testing.B) {
	for _, n := range []int{3000, 6000} {
		keys, builtin, persistent := benchMaps(n)
		b.Run(fmt.Sprintf("builtin/%d", n), func(b *testing.B) {
			for i := 0; b.Loop(); i++ {
				if builtin[keys[i%n]] != nil {
					benchSink++
				}
			}
		})
		b.Run(fmt.Sprintf("persistent/%d", n), func(b *testing.B) {
			for i := 0; b.Loop(); i++ {
				if v, _ := persistent.Get(keys[i%n]); v != nil {
					benchSink++
				}
			}
		})
	}
}

func BenchmarkPersistentMapAll(b *testing.B) {
	_, builtin, persistent := benchMaps(3000)
	b.Run("builtin", func(b *testing.B) {
		for b.Loop() {
			for _, v := range builtin {
				if v != nil {
					benchSink++
				}
			}
		}
	})
	b.Run("persistent", func(b *testing.B) {
		for b.Loop() {
			for _, v := range persistent.All() {
				if v != nil {
					benchSink++
				}
			}
		}
	})
}

// BenchmarkPersistentMapCopyAndEdit is the catalog's per-statement pattern:
// copy the whole map, then change a few entries.
func BenchmarkPersistentMapCopyAndEdit(b *testing.B) {
	keys, builtin, persistent := benchMaps(3000)
	for _, changes := range []int{1, 5, 100} {
		b.Run(fmt.Sprintf("builtin-clone/%d", changes), func(b *testing.B) {
			for b.Loop() {
				c := maps.Clone(builtin)
				for i := range changes {
					c[keys[i]] = nil
				}
			}
		})
		b.Run(fmt.Sprintf("persistent-set/%d", changes), func(b *testing.B) {
			for b.Loop() {
				c := persistent
				for i := range changes {
					c = c.Set(keys[i], nil)
				}
			}
		})
		b.Run(fmt.Sprintf("persistent-setall/%d", changes), func(b *testing.B) {
			for b.Loop() {
				_ = persistent.SetAll(func(yield func(benchAddress, *int) bool) {
					for i := range changes {
						if !yield(keys[i], nil) {
							return
						}
					}
				})
			}
		})
	}
}

func BenchmarkPersistentMapBuild(b *testing.B) {
	keys, builtin, _ := benchMaps(3000)
	b.Run("builtin", func(b *testing.B) {
		for b.Loop() {
			m := make(map[benchAddress]*int, len(keys))
			for _, k := range keys {
				m[k] = nil
			}
		}
	})
	b.Run("persistent-from", func(b *testing.B) {
		for b.Loop() {
			_ = PersistentMapFrom(func(yield func(benchAddress, *int) bool) {
				for _, k := range keys {
					if !yield(k, nil) {
						return
					}
				}
			})
		}
	})
	b.Run("persistent-of-map", func(b *testing.B) {
		for b.Loop() {
			_ = PersistentMapOf(builtin)
		}
	})
	b.Run("persistent-transient", func(b *testing.B) {
		for b.Loop() {
			t := PersistentMap[benchAddress, *int]{}.Transient()
			for _, k := range keys {
				t.Set(k, nil)
			}
			_ = t.Persistent()
		}
	})
}
