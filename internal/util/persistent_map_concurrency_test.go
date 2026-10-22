package util

import (
	"maps"
	"sync"
	"testing"
)

// TestPersistentMapConcurrentReaders reads one version from several goroutines
// while others derive new versions from it, persistently and through
// Transients. Readers must always see the original contents; run with -race.
func TestPersistentMapConcurrentReaders(t *testing.T) {
	model := rangeModel(0, 2000, 1)
	shared := PersistentMapFrom(maps.All(model))
	var wg sync.WaitGroup
	errs := make(chan string, 16)
	for reader := range 4 {
		wg.Go(func() {
			for round := range 20 {
				count := 0
				for k, v := range shared.All() {
					if v != 1 || k < 0 || k >= 2000 {
						errs <- "reader saw a changed entry"
						return
					}
					count++
				}
				if count != 2000 || shared.Len() != 2000 {
					errs <- "reader saw a changed size"
					return
				}
				if v, ok := shared.Get((reader*97 + round) % 2000); !ok || v != 1 {
					errs <- "reader lost a key"
					return
				}
			}
		})
	}
	for writer := range 4 {
		wg.Go(func() {
			derived := shared
			for k := range 2000 {
				if (k+writer)%2 == 0 {
					derived = derived.Set(k, writer+2).Delete(k + 1)
				}
			}
			tr := shared.Transient()
			for k := range 2000 {
				tr.Set(k, -writer)
				tr.Delete(k + writer)
			}
			_ = tr.Persistent()
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	checkModel(t, shared, realHash, model)
}
