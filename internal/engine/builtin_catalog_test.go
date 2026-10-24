package engine

import (
	"cmp"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/settings"
)

// Production validates the built-in seed once per process; every engine
// configuration must still seed a catalog that passes full validation.
func TestBuiltinCatalogValidatesForEveryEngineConfiguration(t *testing.T) {
	setting, err := settings.Validate(settings.Definition{Name: "seedcheck.mode", Type: settings.String, BootDefault: "on"})
	if err != nil {
		t.Fatal(err)
	}
	for _, config := range []struct {
		definitions []settings.Definition
		database    string
		commitTimes bool
	}{
		{nil, "", false},
		{[]settings.Definition{setting}, "seed_database", true},
		{nil, strings.Repeat("d", 80), false},
	} {
		e := NewWithSettings(config.definitions, config.database, config.commitTimes)
		if err := e.validateCatalogDescriptors(e.catalog.State.Descriptors()); err != nil {
			t.Fatalf("database %q: %v", config.database, err)
		}
	}
}

func TestBuiltinProcedureNamesMatchSeed(t *testing.T) {
	for range 2 {
		e := New()
		got, want := e.catalog.State.ProcedureNames(), catalog.RebuildProcedureNames(e.catalog.State.Descriptors())
		if len(got.Buckets) != len(want.Buckets) || !maps.Equal(got.DynamicNames, want.DynamicNames) {
			t.Fatalf("procedure index has %d buckets, seed has %d", len(got.Buckets), len(want.Buckets))
		}
		for key, addresses := range want.Buckets {
			if !sameAddresses(got.Buckets[key], addresses) {
				t.Fatalf("procedure bucket %v = %v, want %v", key, got.Buckets[key], addresses)
			}
		}
	}
}

// Engines share the seeded procedure-name index; a staged procedure change
// must copy it, so concurrent engines never observe each other's changes.
func TestBuiltinProcedureNamesCopyOnEdit(t *testing.T) {
	engines := []*Engine{New(), New()}
	shared := engines[0].catalog.State.ProcedureNames()
	if engines[1].catalog.State.ProcedureNames() != shared {
		t.Fatal("precondition: engines do not share the seeded procedure index")
	}
	public := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	key := catalog.ProcedureNameKey{Namespace: public, Name: "seed_copy"}
	builtin := catalog.ProcedureNameKey{Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, Name: "abs"}
	builtinBucket := slices.Clone(shared.Buckets[builtin])
	if len(builtinBucket) == 0 {
		t.Fatal("precondition: missing built-in abs overloads")
	}
	createdAddresses := make([]ids.ObjectAddress, len(engines))
	for i, e := range engines {
		e.mu.Lock()
		oid, err := e.catalog.AllocateOIDLocked()
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		createdAddresses[i] = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
	}
	next := make([]*catalog.State, len(engines))
	errs := make([]error, len(engines))
	var wg sync.WaitGroup
	for i, e := range engines {
		wg.Go(func() {
			candidate := catalog.NewCandidate(e.CatalogRead())
			created := &catalog.Procedure{ObjectAddress: createdAddresses[i], ObjectNamespace: public, ObjectRevision: 1, ObjectName: key.Name, Signature: "23", RoutineKind: catalog.FunctionRoutine}
			moved := *e.CatalogRead().MustProcedure(builtinBucket[0])
			moved.ObjectNamespace = public
			if errs[i] = candidate.Create(created); errs[i] == nil {
				errs[i] = candidate.Replace(&moved)
			}
			next[i] = candidate.State()
		})
	}
	wg.Wait()
	for i, state := range next {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		created := createdAddresses[i]
		if got := state.ProceduresNamed(public, key.Name); len(got) != 1 || got[0] != created {
			t.Fatalf("engine %d staged procedures %v, want only %v", i, got, created)
		}
		if got := state.ProceduresNamed(builtin.Namespace, builtin.Name); slices.Contains(got, builtinBucket[0]) {
			t.Fatalf("engine %d kept a moved procedure in its old bucket", i)
		}
	}
	if shared.Buckets[key] != nil || shared.Buckets[catalog.ProcedureNameKey{Namespace: public, Name: builtin.Name}] != nil || !slices.Equal(shared.Buckets[builtin], builtinBucket) {
		t.Fatal("a staged procedure change wrote the shared seed index")
	}
}

func sameAddresses(a, b []ids.ObjectAddress) bool {
	key := func(a ids.ObjectAddress) uint64 { return uint64(a.ClassID)<<32 | uint64(a.ObjectID) }
	sorted := func(s []ids.ObjectAddress) []ids.ObjectAddress {
		s = slices.Clone(s)
		slices.SortFunc(s, func(x, y ids.ObjectAddress) int { return cmp.Compare(key(x), key(y)) })
		return s
	}
	return slices.Equal(sorted(a), sorted(b))
}

// Engines share the seed's name map. Every configuration must end with
// exactly the names its own seed bound: a seed built with a fresh share builds
// its map from its own bindings, which the shared result must match.
func TestBuiltinSeedNamesMatchUnsharedSeed(t *testing.T) {
	setting, err := settings.Validate(settings.Definition{Name: "seedcheck.names", Type: settings.String, BootDefault: "on"})
	if err != nil {
		t.Fatal(err)
	}
	_ = New()
	for _, config := range []struct {
		definitions []settings.Definition
		database    string
		commitTimes bool
	}{
		{nil, "", false},
		{[]settings.Definition{setting}, "seed_names", true},
		{nil, strings.Repeat("n", 80), false},
	} {
		shared := maps.Collect(NewWithSettings(config.definitions, config.database, config.commitTimes).catalog.State.Names())
		saved := builtinSeedNames
		builtinSeedNames = &seedNames{}
		own := maps.Collect(NewWithSettings(config.definitions, config.database, config.commitTimes).catalog.State.Names())
		builtinSeedNames = saved
		if !maps.Equal(shared, own) {
			t.Fatalf("database %q: shared seed has %d names, own seed %d", config.database, len(shared), len(own))
		}
	}
}
