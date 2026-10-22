package catalog

import (
	"fmt"
	"testing"

	"stuntgres/internal/engine/ids"
)

func catalogTestRelation(oid ids.OID, ns ids.ObjectAddress) *Relation {
	return &Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, ObjectNamespace: ns, ObjectName: fmt.Sprintf("r%d", oid), ObjectRevision: 1}
}

// TestCatalogDescriptorsVisitOnceWhileWriting replaces and removes entries of
// the state it iterates, as whole-catalog rewrites do. Every descriptor
// present at the start is visited exactly once, and the writes all apply.
func TestCatalogDescriptorsVisitOnceWhileWriting(t *testing.T) {
	base, ns, _ := catalogReadFixture()
	for oid := ids.OID(1000); oid < 1200; oid++ {
		r := catalogTestRelation(oid, ns.Address())
		base.SetDescriptor(r.Address(), r)
	}
	want := base.DescriptorCount()
	visits := map[ids.ObjectAddress]int{}
	for address, d := range base.Descriptors() {
		visits[address]++
		r, ok := d.(*Relation)
		if !ok {
			continue
		}
		if address.ObjectID%3 == 0 {
			base.RemoveDescriptor(address)
			continue
		}
		next := *r
		next.ObjectRevision++
		base.SetDescriptor(address, &next)
	}
	if len(visits) != want {
		t.Fatalf("visited %d descriptors, want %d", len(visits), want)
	}
	for address, n := range visits {
		if n != 1 {
			t.Fatalf("visited %v %d times", address, n)
		}
	}
	for address, d := range base.Descriptors() {
		if r, ok := d.(*Relation); ok && (address.ObjectID%3 == 0 || r.ObjectRevision != 2) {
			t.Fatalf("write during iteration did not apply to %v", address)
		}
	}
}

func TestCatalogBulkLoad(t *testing.T) {
	base, ns, rel := catalogReadFixture()
	published := CloneState(base)
	base.BeginBulkLoad(16)
	if base.Get(rel.Address()) != rel || base.DescriptorCount() != 2 || base.NameAddress(DescriptorNameKey(rel)) != rel.Address() {
		t.Fatal("a bulk load hid the existing entries")
	}
	added := catalogTestRelation(2000, ns.Address())
	base.SetDescriptor(added.Address(), added)
	base.BindName(DescriptorNameKey(added), added.Address())
	base.RemoveDescriptor(rel.Address())
	base.UnbindName(DescriptorNameKey(rel))
	if base.Get(added.Address()) != added || base.Get(rel.Address()) != nil || base.NameCount() != 2 {
		t.Fatal("reads during a bulk load missed its writes")
	}
	seen := 0
	for range base.Descriptors() {
		seen++
	}
	if seen != 2 {
		t.Fatalf("iteration during a bulk load saw %d descriptors", seen)
	}
	for name, use := range map[string]func(){
		"CloneState":      func() { CloneState(base) },
		"Candidate.State": func() { (&Candidate{Base: Read{state: base}}).State() },
		"ResetMaps":       func() { base.ResetMaps() },
		"BeginBulkLoad":   func() { base.BeginBulkLoad(0) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("%s during a bulk load did not panic", name)
				}
			}()
			use()
		}()
	}
	base.EndBulkLoad()
	if base.Get(added.Address()) != added || base.Get(rel.Address()) != nil || base.DescriptorCount() != 2 || base.NameAddress(DescriptorNameKey(added)) != added.Address() {
		t.Fatal("EndBulkLoad lost the loaded entries")
	}
	if published.Get(rel.Address()) != rel || published.Get(added.Address()) != nil {
		t.Fatal("a bulk load changed a copy taken before it")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("EndBulkLoad without a bulk load did not panic")
		}
	}()
	base.EndBulkLoad()
}
