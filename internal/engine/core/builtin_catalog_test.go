package core_test

import (
	"cmp"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
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
		e := engine.NewWithSettings(config.definitions, config.database, config.commitTimes)
		if err := e.Catalog.ValidateDescriptors(e.Catalog.State.Descriptors()); err != nil {
			t.Fatalf("database %q: %v", config.database, err)
		}
	}
}

func TestBuiltinProcedureNamesMatchSeed(t *testing.T) {
	for range 2 {
		e := engine.New()
		got, want := e.Catalog.State.ProcedureNames(), catalog.RebuildProcedureNames(e.Catalog.State.Descriptors())
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
	engines := []*core.Engine{engine.New(), engine.New()}
	shared := engines[0].Catalog.State.ProcedureNames()
	if engines[1].Catalog.State.ProcedureNames() != shared {
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
		e.Mu.Lock()
		oid, err := e.Catalog.AllocateOIDLocked()
		e.Mu.Unlock()
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
	_ = engine.New()
	for _, config := range []struct {
		definitions []settings.Definition
		database    string
		commitTimes bool
	}{
		{nil, "", false},
		{[]settings.Definition{setting}, "seed_names", true},
		{nil, strings.Repeat("n", 80), false},
	} {
		shared := maps.Collect(engine.NewWithSettings(config.definitions, config.database, config.commitTimes).Catalog.State.Names())
		saved := core.BuiltinSeed()
		*core.BuiltinSeedRef() = &catalog.SeedShare{}
		own := maps.Collect(engine.NewWithSettings(config.definitions, config.database, config.commitTimes).Catalog.State.Names())
		*core.BuiltinSeedRef() = saved
		if !maps.Equal(shared, own) {
			t.Fatalf("database %q: shared seed has %d names, own seed %d", config.database, len(shared), len(own))
		}
	}
	if seed := core.BuiltinSeed(); !seed.SharesNames() || !seed.SharesDescriptors() || seed.Fallbacks() != 0 {
		t.Fatal("engine seeds did not follow the shared seed", seed.SharesNames(), seed.SharesDescriptors(), seed.Fallbacks())
	}
}

// A seed that follows the shared names reads names during the seed as its own
// seed would: row types, domain checks and defaults come out the same. Every
// seeded descriptor holds exactly the name its descriptor key gives.
func TestSharedSeedMatchesUnsharedSeedDescriptors(t *testing.T) {
	_ = engine.New()
	shared := engine.New().Catalog.State
	saved := core.BuiltinSeed()
	*core.BuiltinSeedRef() = &catalog.SeedShare{}
	own := engine.New().Catalog.State
	*core.BuiltinSeedRef() = saved
	for _, state := range []*catalog.State{shared, own} {
		if state.NameCount() != state.DescriptorCount() {
			t.Fatal("seed names do not bind one name per descriptor", state.NameCount(), state.DescriptorCount())
		}
		for k, a := range state.Names() {
			if catalog.DescriptorNameKey(state.Get(a)) != k {
				t.Fatalf("name %v does not name %v", k, a)
			}
		}
	}
	if shared.DescriptorCount() != own.DescriptorCount() {
		t.Fatal("shared and own seeds hold different descriptor counts", shared.DescriptorCount(), own.DescriptorCount())
	}
	for a, d := range shared.Descriptors() {
		if o := own.Get(a); o == nil || o.Name() != d.Name() || o.Kind() != d.Kind() {
			t.Fatalf("descriptor %v differs from its own seed", a)
		}
		switch d := d.(type) {
		case *catalog.Type:
			o, ok := own.Get(a).(*catalog.Type)
			if !ok || o.OwningRelation != d.OwningRelation || len(o.DomainChecks) != len(d.DomainChecks) || (o.DomainDefault == nil) != (d.DomainDefault == nil) {
				t.Fatalf("type %v differs from its own seed", a)
			}
		case *catalog.Relation:
			if o, ok := own.Get(a).(*catalog.Relation); !ok || o.RowType != d.RowType {
				t.Fatalf("relation %v differs from its own seed", a)
			}
		}
	}
}

// Each engine's catalog sources are its own relations, typed by its own type
// generation and owned by its own principals, with the same shape in every
// engine.
func TestCatalogSourcesUseEngineTypes(t *testing.T) {
	first, second := engine.New().CatalogRead(), engine.New().CatalogRead()
	for _, spec := range pgcatalog.Specs {
		address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: spec.OID}
		a, ok := first.Get(address).(*catalog.Relation)
		b, found := second.Get(address).(*catalog.Relation)
		if !ok || !found || a == b || len(a.Columns) != len(spec.Columns) || len(b.Columns) != len(spec.Columns) || a.Owner == b.Owner {
			t.Fatalf("%s: engines do not hold their own catalog source", spec.Name)
		}
		for i, column := range spec.Columns {
			want, _ := first.LookupType(column.Type)
			if a.Columns[i].Usage.Type != want || b.Columns[i].Usage.Type == want || a.Columns[i].Name != b.Columns[i].Name || a.Columns[i].Usage.Typmod != b.Columns[i].Usage.Typmod || a.Columns[i].Usage.Collation != b.Columns[i].Usage.Collation {
				t.Fatalf("%s.%s: column is not typed by its engine", spec.Name, column.Name)
			}
		}
		if len(a.ColumnMeta.CatalogAttributes) != len(spec.Columns) || a.RowType != b.RowType {
			t.Fatalf("%s: catalog attribute facts or row type differ", spec.Name)
		}
	}
}