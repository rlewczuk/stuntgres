package catalog

import (
	"maps"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// seedFixture seeds namespaces, tablespaces, access methods and a database
// named database with share, after the steps of variant (seedSteps).
func seedFixture(share *SeedShare, database, variant string) *Store {
	s := newSeedStore(share)
	for _, step := range seedSteps(variant) {
		step(s.State)
	}
	finishSeedFixture(s, share, database)
	return s
}

func newSeedStore(share *SeedShare) *Store {
	s := NewStore(Owner{Providers: map[ids.OID]Provider{}}, 16384, 2)
	s.PublishLocked(NewState(1))
	s.BeginSeedLocked(share, 16)
	return &s
}

// finishSeedFixture binds the database like the engine (a plain bind) and
// finishes the seed.
func finishSeedFixture(s *Store, share *SeedShare, database string) {
	domain := &authorization.Domain{}
	db := &Database{ObjectAddress: s.DatabaseAddress(), ObjectRevision: 1, ObjectName: database, Owner: domain.Principal(authorization.BootstrapPrincipalID)}
	s.State.SetDescriptor(db.Address(), db)
	s.State.BindName(DescriptorNameKey(db), db.Address())
	s.FinishSeedLocked(share)
}

// seedSteps returns the install steps of a test seed. "" is the recorded
// order; the others diverge from it: "extra" binds one more namespace,
// "missing" skips tablespaces, "reordered" installs tablespaces first,
// "rebind" moves a seeded name and "unbind" removes one.
func seedSteps(variant string) []func(*State) {
	domain := &authorization.Domain{}
	owner := domain.Principal(authorization.BootstrapPrincipalID)
	namespaces := func(s *State) { InstallBuiltinNamespaces(s, owner, domain.Principal(6171)) }
	tablespaces := func(s *State) { InstallBuiltinTablespaces(s, owner) }
	methods := func(s *State) { InstallBuiltinAccessMethods(s) }
	public := ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"}
	switch variant {
	case "extra":
		return []func(*State){namespaces, tablespaces, func(s *State) {
			installSeedDescriptor(s, &Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 99999}, ObjectName: "seed_extra", ObjectRevision: 1, Owner: owner})
		}, methods}
	case "missing":
		return []func(*State){namespaces, methods}
	case "reordered":
		return []func(*State){tablespaces, namespaces, methods}
	case "rebind":
		return []func(*State){namespaces, func(s *State) { s.BindName(public, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 99998}) }, tablespaces, methods}
	case "unbind":
		return []func(*State){namespaces, func(s *State) { s.UnbindName(public) }, tablespaces, methods}
	}
	return []func(*State){namespaces, tablespaces, methods}
}

// A seed finished with a filled share ends with exactly the names it bound,
// as if it had built its own map, including when it bound different names.
func TestSeedShareNamesMatchOwnSeed(t *testing.T) {
	share := &SeedShare{}
	first := seedFixture(share, "first", "")
	if !share.SharesNames() || share.descriptors.Load() == nil {
		t.Fatal("the first seed did not record its names and descriptors")
	}
	for _, c := range []struct{ database, variant string }{
		{"first", ""}, {"second", ""}, {"third", "extra"}, {"fourth", "missing"},
		{"fifth", "reordered"}, {"sixth", "rebind"}, {"seventh", "unbind"},
	} {
		before := share.Fallbacks()
		sharedSeed, ownSeed := seedFixture(share, c.database, c.variant), seedFixture(&SeedShare{}, c.database, c.variant)
		shared, own := maps.Collect(sharedSeed.State.Names()), maps.Collect(ownSeed.State.Names())
		if !maps.Equal(shared, own) {
			t.Fatalf("%q/%q: shared seed has %d names, own seed %d", c.database, c.variant, len(shared), len(own))
		}
		if !maps.Equal(seedDescriptorSummary(sharedSeed.State), seedDescriptorSummary(ownSeed.State)) {
			t.Fatalf("%q/%q: shared seed descriptors differ from an own seed", c.database, c.variant)
		}
		if fell := share.Fallbacks() - before; fell != 0 != (c.variant != "") {
			t.Fatalf("%q/%q: %d fallbacks", c.database, c.variant, fell)
		}
	}
	if _, ok := first.State.LookupName(ids.NameKey{ClassID: ids.DatabaseClassID, Name: "second"}); ok {
		t.Fatal("a later seed's database binding reached the first seed")
	}
	_ = CloneState(first.State) // A state still in its bulk load panics on copy.
}

// A catalog source takes ownership of its same-named seeded row type and that
// type's array.
func TestInstallCatalogSourceOwnsRowType(t *testing.T) {
	domain := &authorization.Domain{}
	state := NewState(1)
	InstallBuiltinTypes(state, sqltype.Builtins(), domain.Principal(authorization.BootstrapPrincipalID))
	pgCatalog := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	address, ok := state.LookupName(ids.NameKey{ClassID: ids.TypeClassID, Namespace: pgCatalog, Name: "pg_class"})
	if !ok || state.MustType(address).Array == 0 {
		t.Fatal("precondition: missing seeded pg_class row type with an array")
	}
	owner := domain.Principal(42)
	d := &Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1259}, ObjectNamespace: pgCatalog, ObjectName: "pg_class", ObjectRevision: 1, RelationKind: CatalogRelation, Owner: owner}
	InstallCatalogSource(state, d)
	row := state.MustType(address)
	array := state.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)})
	if d.RowType != row.SQLType.ID() || row.OwningRelation != d.Address() || row.Owner != owner || array.OwningRelation != d.Address() || array.Owner != owner {
		t.Fatalf("row type %+v, array %+v, relation row type %d", row.OwningRelation, array.OwningRelation, d.RowType)
	}
	if bound, ok := state.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: pgCatalog, Name: "pg_class"}); !ok || bound != d.Address() {
		t.Fatal("catalog source name is not bound")
	}
}
