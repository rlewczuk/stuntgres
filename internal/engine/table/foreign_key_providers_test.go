package table

import (
	"errors"
	"maps"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// foreignKeyFixture is p, q and their referencing tables in one namespace:
// c → p, s → s (self) and r → q. Its view is the base catalog.
type foreignKeyFixture struct {
	view      catalog.Read
	domain    *ids.Domain
	namespace ids.ObjectAddress
	p, q      *catalog.Relation
	c, s, r   *catalog.Relation
}

const foreignKeyEpoch ids.CatalogEpoch = 5

func foreignKeyRelation(namespace ids.ObjectAddress, oid ids.OID, name string, targets ...ids.ObjectAddress) *catalog.Relation {
	d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, ObjectNamespace: namespace, ObjectName: name, RelationKind: catalog.TableRelation}
	for i, target := range targets {
		d.Constraints.ForeignKeys = append(d.Constraints.ForeignKeys, catalog.ForeignKeyConstraint{OID: oid*10 + ids.OID(i), Name: name + "_fk", Attributes: []int32{1}, Target: target, Referenced: []int32{1}})
	}
	return d
}

func newForeignKeyFixture() foreignKeyFixture {
	f := foreignKeyFixture{domain: &ids.Domain{}, namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200}}
	f.p = foreignKeyRelation(f.namespace, 100, "p")
	f.c = foreignKeyRelation(f.namespace, 101, "c", f.p.Address())
	f.s = foreignKeyRelation(f.namespace, 102, "s")
	f.s.Constraints.ForeignKeys = foreignKeyRelation(f.namespace, 102, "s", f.s.Address()).Constraints.ForeignKeys
	f.q = foreignKeyRelation(f.namespace, 103, "q")
	f.r = foreignKeyRelation(f.namespace, 104, "r", f.q.Address())
	state := catalog.NewState(foreignKeyEpoch)
	state.SetDescriptor(f.namespace, &catalog.Namespace{ObjectAddress: f.namespace})
	for _, d := range []*catalog.Relation{f.r, f.q, f.s, f.c, f.p} {
		state.SetDescriptor(d.Address(), d)
		state.BindName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: f.namespace, Name: d.ObjectName}, d.Address())
	}
	f.view = catalog.NewRead(nil, state)
	return f
}

func (f foreignKeyFixture) refs(relations ...*catalog.Relation) []ids.ObjectRef {
	var refs []ids.ObjectRef
	for _, d := range relations {
		refs = append(refs, f.domain.Ref(foreignKeyEpoch, d.Address()))
	}
	return refs
}

func TestForeignKeyTypeProviders(t *testing.T) {
	f := newForeignKeyFixture()
	for _, tc := range []struct {
		name    string
		address ids.ObjectAddress
		want    []ids.ObjectRef
	}{
		{"referenced", f.p.Address(), f.refs(f.p, f.c)},
		{"referencing", f.c.Address(), f.refs(f.p, f.c)},
		{"self", f.s.Address(), f.refs(f.s)},
		{"other tree", f.q.Address(), f.refs(f.q, f.r)},
		{"unrelated", ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 999}, nil},
	} {
		if got := ForeignKeyTypeProviders(f.view, f.domain, tc.address); !slices.Equal(got, tc.want) {
			t.Errorf("%s: providers %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The staged descriptors are a new table n (→ p, → n itself and → a new m)
// and a staged copy of c that keeps its installed key and adds one → q.
func TestForeignKeyDefinitionProviders(t *testing.T) {
	f := newForeignKeyFixture()
	m := foreignKeyRelation(f.namespace, 106, "m")
	n := foreignKeyRelation(f.namespace, 105, "n", f.p.Address(), ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 105}, m.Address())
	c := *f.c
	c.Constraints.ForeignKeys = append(slices.Clone(f.c.Constraints.ForeignKeys), foreignKeyRelation(f.namespace, 101, "c2", f.q.Address()).Constraints.ForeignKeys...)
	staged := map[ids.ObjectAddress]catalog.Descriptor{n.Address(): n, m.Address(): m, c.Address(): &c}
	if !InstalledForeignKey(f.view, c.Address(), c.Constraints.ForeignKeys[0]) || InstalledForeignKey(f.view, c.Address(), c.Constraints.ForeignKeys[1]) {
		t.Fatal("installed key detection")
	}
	changed := c.Constraints.ForeignKeys[0]
	changed.NotValid = true
	if InstalledForeignKey(f.view, c.Address(), changed) || InstalledForeignKey(f.view, n.Address(), n.Constraints.ForeignKeys[0]) {
		t.Fatal("changed or new key treated as installed")
	}
	if got, want := ForeignKeyDefinitionProviders(f.view, f.domain, maps.All(staged)), f.refs(f.p, f.q); !slices.Equal(got, want) {
		t.Fatalf("providers %v, want %v", got, want)
	}
	delete(staged, c.Address())
	n.Constraints.ForeignKeys = n.Constraints.ForeignKeys[1:]
	if got := ForeignKeyDefinitionProviders(f.view, f.domain, maps.All(staged)); len(got) != 0 {
		t.Fatalf("self and staged targets need no provider: %v", got)
	}
}

func TestForeignKeyChecks(t *testing.T) {
	f := newForeignKeyFixture()
	c := *f.c
	c.Constraints.ForeignKeys = append(slices.Clone(f.c.Constraints.ForeignKeys), catalog.ForeignKeyConstraint{OID: 7, Target: f.q.Address(), NotValid: true}, catalog.ForeignKeyConstraint{OID: 8, Target: f.q.Address()})
	added, err := ResolveAlterForeignKeys(&c, c.Constraints.ForeignKeys[len(c.Constraints.ForeignKeys)-1:], nil)
	if err != nil || len(added) != 1 || added[0].Child != &c || added[0].Key.OID != 8 {
		t.Fatalf("added checks %+v", added)
	}
	if _, err := ResolveAlterForeignKeys(&c, c.Constraints.ForeignKeys[len(c.Constraints.ForeignKeys)-2:len(c.Constraints.ForeignKeys)-1], nil); err == nil {
		t.Fatal("scheduled invalid FK was silently dropped instead of rejected")
	}
	keys := func(checks []ForeignKeyCheck) []ids.OID {
		var oids []ids.OID
		for _, check := range checks {
			oids = append(oids, check.Key.OID)
		}
		slices.Sort(oids)
		return oids
	}
	if got := keys(ReferencingForeignKeys(f.view, f.p.Address())); !slices.Equal(got, []ids.OID{1010}) {
		t.Fatalf("keys referencing p %v", got)
	}
	if got := keys(ReferencingForeignKeys(f.view, f.c.Address())); !slices.Equal(got, []ids.OID{1010}) {
		t.Fatalf("keys of c %v", got)
	}
	if got := keys(ReferencingForeignKeys(f.view, f.s.Address())); !slices.Equal(got, []ids.OID{1020}) {
		t.Fatalf("keys of s %v", got)
	}
}

// A missing parent or index is a replaced catalog, found before any capture.
func TestScanForeignKeysReplaced(t *testing.T) {
	f := newForeignKeyFixture()
	captured := 0
	v := ForeignKeyScan{Domain: f.domain, Capture: func(ids.ObjectRef, ids.ObjectAddress) (ForeignKeyRows, error) {
		captured++
		return ForeignKeyRows{}, nil
	}}
	v.View.Catalog = f.view
	missingParent := catalog.ForeignKeyConstraint{OID: 1, Target: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 999}}
	missingIndex := catalog.ForeignKeyConstraint{OID: 2, Target: f.p.Address(), Index: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 998}}
	for _, key := range []catalog.ForeignKeyConstraint{missingParent, missingIndex} {
		if err := ScanForeignKeys(t.Context(), v, []ForeignKeyCheck{{Child: f.c, Key: key}}); !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatalf("key %d: %v", key.OID, err)
		}
	}
	if err := ScanForeignKeys(t.Context(), v, nil); err != nil {
		t.Fatal(err)
	}
	if captured != 0 {
		t.Fatalf("captured %d row sets", captured)
	}
}

// The bound slot must still hold the bound key; NOT VALID keys are not scanned.
func TestScanRestoredForeignKeysSlots(t *testing.T) {
	f := newForeignKeyFixture()
	captured := 0
	v := ForeignKeyScan{Domain: f.domain, Capture: func(ids.ObjectRef, ids.ObjectAddress) (ForeignKeyRows, error) {
		captured++
		return ForeignKeyRows{}, nil
	}}
	v.View.Catalog = f.view
	c := *f.c
	c.Constraints.ForeignKeys = []catalog.ForeignKeyConstraint{{OID: 9, Target: f.p.Address(), NotValid: true}}
	if err := ScanRestoredForeignKeys(t.Context(), v, &c, []RestoredForeignKey{{BoundSlot: 0, BoundOID: 9}}); err != nil {
		t.Fatal(err)
	}
	for _, definition := range []RestoredForeignKey{{BoundSlot: 1, BoundOID: 9}, {BoundSlot: -1, BoundOID: 9}, {BoundSlot: 0}, {BoundSlot: 0, BoundOID: 8}} {
		if err := ScanRestoredForeignKeys(t.Context(), v, &c, []RestoredForeignKey{definition}); !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatalf("definition %+v: %v", definition, err)
		}
	}
	if captured != 0 {
		t.Fatalf("captured %d row sets", captured)
	}
}

func TestRestoredForeignKeyTarget(t *testing.T) {
	f := newForeignKeyFixture()
	other := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2300}
	name := func(parts ...string) parser.ObjectName {
		var n parser.ObjectName
		for _, part := range parts {
			n.Parts = append(n.Parts, parser.Identifier{Text: part})
		}
		return n
	}
	missing := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 999}
	for _, tc := range []struct {
		name       string
		definition RestoredForeignKey
		want       ids.ObjectAddress
		replaced   bool
		undefined  string
	}{
		{name: "captured", definition: RestoredForeignKey{Raw: parser.ForeignKeyConstraint{Target: name("q")}, Target: f.p.Address()}, want: f.p.Address()},
		{name: "captured replaced", definition: RestoredForeignKey{Raw: parser.ForeignKeyConstraint{Target: name("p")}, Target: missing}, replaced: true},
		{name: "path", definition: RestoredForeignKey{Raw: parser.ForeignKeyConstraint{Target: name("x", "q")}, Path: []ids.ObjectAddress{other, f.namespace}}, want: f.q.Address()},
		{name: "undefined", definition: RestoredForeignKey{Raw: parser.ForeignKeyConstraint{Target: name("x", "z")}, Path: []ids.ObjectAddress{other, f.namespace}}, undefined: `relation "x.z" does not exist`},
		{name: "no path", definition: RestoredForeignKey{Raw: parser.ForeignKeyConstraint{Target: name("p")}}, undefined: `relation "p" does not exist`},
	} {
		got, err := tc.definition.ResolveTarget(f.view)
		var diagnostic *sqlstate.Error
		switch {
		case tc.replaced:
			if !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Errorf("%s: %v", tc.name, err)
			}
		case tc.undefined != "":
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42P01" || diagnostic.Message != tc.undefined {
				t.Errorf("%s: %v", tc.name, err)
			}
		case err != nil || got != tc.want:
			t.Errorf("%s: %v, %v; want %v", tc.name, got, err, tc.want)
		}
	}
}
