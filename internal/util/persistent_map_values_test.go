package util

import (
	"fmt"
	"maps"
	"testing"
)

// checkValues checks that mapped has m's shape and keys with f's values,
// that m is unchanged, and that edits of either map stay out of the other.
func checkValues(t *testing.T, m PersistentMap[int, int], h hashFunc[int], model map[int]int) {
	t.Helper()
	f := func(k, v int) string { return fmt.Sprint(k, ":", v) }
	mapped := PersistentMapValues(m, f)
	want := map[int]string{}
	for k, v := range model {
		want[k] = f(k, v)
	}
	if mapped.Len() != len(want) || shape(mapped) != shape(m) {
		t.Fatalf("mapped shape differs:\n%s\n%s", shape(mapped), shape(m))
	}
	for k, v := range want {
		if got, ok := forcedGet(mapped, h, k); !ok || got != v {
			t.Fatalf("mapped Get(%d) = %q, %v; want %q", k, got, ok, v)
		}
	}
	checkTrie(t, mapped, h)
	checkModel(t, m, h, model)
	if len(model) == 0 {
		return
	}
	for k := range model {
		edited := forcedSet(mapped, h, k, "edited")
		edited = forcedSet(edited, h, -1-k, "new")
		edited = forcedDelete(edited, h, k+1)
		checkTrie(t, edited, h)
		break
	}
	checkModel(t, m, h, model)
	for k, v := range want {
		if got, _ := forcedGet(mapped, h, k); got != v {
			t.Fatalf("an edit of a copy changed mapped key %d to %q", k, got)
		}
	}
}

func TestPersistentMapValues(t *testing.T) {
	hashes := maps.Clone(forcedHashes)
	hashes["real"] = realHash[int]
	for name, h := range hashes {
		for _, n := range []int{0, 1, 2, 33, 1000} {
			t.Run(fmt.Sprint(name, "/", n), func(t *testing.T) {
				var m PersistentMap[int, int]
				model := map[int]int{}
				for k := range n {
					m = forcedSet(m, h, k, k*7)
					model[k] = k * 7
				}
				checkValues(t, m, h, model)
			})
		}
	}
	// A built map, whose slices come from shared arrays, maps the same way.
	built := PersistentMapFrom(func(yield func(int, int) bool) {
		for k := range 5000 {
			if !yield(k, -k) {
				return
			}
		}
	})
	model := map[int]int{}
	for k := range 5000 {
		model[k] = -k
	}
	checkValues(t, built, realHash[int], model)
}
