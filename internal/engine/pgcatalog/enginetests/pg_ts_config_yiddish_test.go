package enginetests_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
)

func yiddishSeedDependencies(t *testing.T) {
	e := engine.New()
	original := e.CatalogRead()
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}
	a := original.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ns, Name: "yiddish"})
	d := original.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ns, Name: "yiddish_stem"})
	if a.ObjectID == 0 || d.ObjectID == 0 { t.Fatal("missing genuine seeds") }
	seed := original.MustTSConfiguration(a)
	state := catalog.CloneState(original.State())
	view := original.WithState(state)
	check := func(want int) {
		t.Helper()
		edges, err := pgcatalog.CollectDepend(context.Background(), view, core.EngineCatalogRuntime{})
		if err != nil { t.Fatal(err) }
		count := 0
		for _, edge := range edges {
			if edge.Dependent != a { continue }
			if edge.Referenced != d || edge.DepType != 'n' { t.Fatal("unexpected allocated seed dependency", edge) }
			count++
		}
		if count != want { t.Fatal("deduplicated six-map frontier", count, want) }
	}
	check(1)
	next := *seed
	next.Mappings = catalog.CloneTSMappings(seed.Mappings)
	next.ObjectRevision++
	state.SetDescriptor(a, &next)
	for _, token := range []int32{1, 2, 10, 11, 16, 17} {
		for i := range next.Mappings { if next.Mappings[i].TokenID == token { next.Mappings[i].Dictionaries = []ids.ObjectAddress{{ClassID: ids.TSDictionaryClassID, ObjectID: 3765}} } }
		want := 1
		if token == 17 { want = 0 }
		check(want)
	}
	// The captured original config remains genuinely mapped to its provider.
	for _, token := range []int32{1, 2, 10, 11, 16, 17} {
		mapping := seed.Mapping(token)
		if len(mapping) != 1 || mapping[0] != d { t.Fatal("frontier mutation changed retained seed", token, mapping) }
	}
}
