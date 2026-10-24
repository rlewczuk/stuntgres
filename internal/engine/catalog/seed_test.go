package catalog

import (
	"maps"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// seedFixture seeds namespaces, tablespaces and a database named database
// with share; extra adds a namespace that other seeds do not bind.
func seedFixture(share *SeedShare, database string, extra bool) *Store {
	domain := &authorization.Domain{}
	owner := domain.Principal(authorization.BootstrapPrincipalID)
	s := NewStore(Owner{Providers: map[ids.OID]Provider{}}, 16384, 2)
	s.PublishLocked(NewState(1))
	s.State.BeginBulkLoad(16)
	InstallBuiltinNamespaces(s.State, owner, domain.Principal(6171))
	InstallBuiltinTablespaces(s.State, owner)
	if extra {
		installSeedDescriptor(s.State, &Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 99999}, ObjectName: "seed_extra", ObjectRevision: 1, Owner: owner})
	}
	installSeedDescriptor(s.State, &Database{ObjectAddress: s.DatabaseAddress(), ObjectRevision: 1, ObjectName: database, Owner: owner})
	s.FinishSeedLocked(share)
	return &s
}

// A seed finished with a filled share ends with exactly the names it bound,
// as if it had built its own map, including when it bound different names.
func TestSeedShareNamesMatchOwnSeed(t *testing.T) {
	share := &SeedShare{}
	first := seedFixture(share, "first", false)
	for _, c := range []struct {
		database string
		extra    bool
	}{{"first", false}, {"second", false}, {"third", true}} {
		shared := maps.Collect(seedFixture(share, c.database, c.extra).State.Names())
		own := maps.Collect(seedFixture(&SeedShare{}, c.database, c.extra).State.Names())
		if !maps.Equal(shared, own) {
			t.Fatalf("database %q: shared seed has %d names, own seed %d", c.database, len(shared), len(own))
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
