package catalog

import (
	"maps"
	"sync"
	"testing"

	"stuntgres/internal/engine/ids"
)

// A seed that follows shared names answers every name read during the seed
// exactly as a seed that binds its own: a shared name is visible only from its
// own bind on, including names bound later and the database binding.
func TestSharedSeedReadsMatchOwnSeed(t *testing.T) {
	share := &SeedShare{}
	reference := seedFixture(share, "reads", "")
	keys := maps.Collect(reference.State.Names())
	keys[ids.NameKey{ClassID: ids.NamespaceClassID, Name: "absent"}] = ids.ObjectAddress{}
	shared, own := newSeedStore(share), newSeedStore(&SeedShare{})
	compare := func(step string) {
		t.Helper()
		if !maps.Equal(maps.Collect(shared.State.Names()), maps.Collect(own.State.Names())) || shared.State.NameCount() != own.State.NameCount() {
			t.Fatalf("%s: names differ (%d shared, %d own)", step, shared.State.NameCount(), own.State.NameCount())
		}
		for k := range keys {
			a, ok := shared.State.LookupName(k)
			b, found := own.State.LookupName(k)
			if a != b || ok != found {
				t.Fatalf("%s: LookupName(%v) = %v %v, own %v %v", step, k, a, ok, b, found)
			}
		}
	}
	compare("start")
	for i, step := range seedSteps("") {
		step(shared.State)
		step(own.State)
		compare(string(rune('a' + i)))
	}
	finishSeedFixture(shared, share, "reads")
	finishSeedFixture(own, &SeedShare{}, "reads")
	compare("finished")
	if share.Fallbacks() != 0 {
		t.Fatal("a matching seed fell back", share.Fallbacks())
	}
}

// Concurrent first seeds of one share all end with their own names, and one of
// them publishes the share for later seeds.
func TestSeedShareConcurrentFirstSeeds(t *testing.T) {
	share := &SeedShare{}
	want := maps.Collect(seedFixture(&SeedShare{}, "race", "").State.Names())
	var wg sync.WaitGroup
	results := make([]map[ids.NameKey]ids.ObjectAddress, 8)
	for i := range results {
		wg.Go(func() { results[i] = maps.Collect(seedFixture(share, "race", "").State.Names()) })
	}
	wg.Wait()
	results = append(results, maps.Collect(seedFixture(share, "race", "").State.Names()))
	for i, got := range results {
		if !maps.Equal(got, want) {
			t.Fatalf("seed %d has %d names, own seed %d", i, len(got), len(want))
		}
	}
	if !share.SharesNames() || share.Fallbacks() != 0 {
		t.Fatal("concurrent first seeds did not publish one matching share", share.SharesNames(), share.Fallbacks())
	}
}
