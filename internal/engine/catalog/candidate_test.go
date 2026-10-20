package catalog

import (
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestCandidatePrivateUpdates(t *testing.T) {
	base, ns, rel := catalogReadFixture()
	rel.History.HasIndexes = true
	rel.History.HasSubclasses = true
	rel.RowType = 42
	rel.Index = map[string]int{"id": 0}
	c := NewCandidate(Read{state: base})
	created := &Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 902}, ObjectNamespace: ns.Address(), ObjectName: "created", ObjectRevision: 2}
	if err := c.Create(created); err != nil {
		t.Fatal(err)
	}
	next := *created
	next.Comment = "private completion"
	c.UpdateDescriptor(&next)
	if c.Get(created.Address()) != &next || created.Comment != "" || base.Get(created.Address()) != nil {
		t.Fatal("created descriptor update replayed creation or changed its source")
	}
	// FK restoration may seed a copied base child before cooking its payload.
	child := *rel
	child.Comment = "private child"
	c.UpdateDescriptor(&child)
	if c.Get(rel.Address()) != &child || base.Get(rel.Address()) != rel || rel.Comment != "" || child.Index["id"] != 0 {
		t.Fatal("base-child update changed the base or lost its payload")
	}
	for _, field := range []string{"indexes", "subclasses"} {
		bad := *rel
		if field == "indexes" {
			bad.History.HasIndexes = false
		} else {
			bad.History.HasSubclasses = false
		}
		if err := c.Replace(&bad); err == nil {
			t.Fatalf("Replace cleared sticky %s history", field)
		}
	}
	replacement := *rel
	replacement.ObjectRevision = 3
	if err := c.Replace(&replacement); err != nil {
		t.Fatal(err)
	}
	got := c.Get(rel.Address()).(*Relation)
	if got.Address() != rel.Address() || got.RowType != 42 || !got.History.HasIndexes || !got.History.HasSubclasses {
		t.Fatal("same-identity replacement lost rowtype or history")
	}
}

func TestCandidateRenameRemovalReuse(t *testing.T) {
	base, ns, rel := catalogReadFixture()
	c := NewCandidate(Read{state: base})
	oldKey := DescriptorNameKey(rel)
	middle := *rel
	middle.ObjectName = "middle"
	middle.ObjectRevision = 2
	if err := c.Replace(&middle); err != nil {
		t.Fatal(err)
	}
	next := middle
	next.ObjectName = "new"
	next.ObjectRevision = 3
	if err := c.Replace(&next); err != nil {
		t.Fatal(err)
	}
	middleKey, newKey := DescriptorNameKey(&middle), DescriptorNameKey(&next)
	if c.LookupName(oldKey) != (ids.ObjectAddress{}) || c.LookupName(middleKey) != (ids.ObjectAddress{}) || c.LookupName(newKey) != rel.Address() {
		t.Fatal("repeated replacement resurrected a vacated name")
	}
	if a, ok := c.NameObservation(oldKey); !ok || a != rel.Address() {
		t.Fatal("lost original base observation")
	}
	if a, ok := c.NameObservation(middleKey); !ok || a != (ids.ObjectAddress{}) {
		t.Fatal("observed intermediate output instead of base absence")
	}
	retained := c.State()
	ref := ids.ObjectRef{Address: rel.Address(), Epoch: 1}
	c.RecordRemoval(ref, rel)
	if r, ok := c.Removal(rel.Address()); !ok || r.Ref != ref || r.Descriptor != rel {
		t.Fatal("retirement lost original ref or descriptor")
	}
	if c.Get(rel.Address()) != nil || c.LookupName(newKey) != (ids.ObjectAddress{}) {
		t.Fatal("retirement retained rebound descriptor or binding")
	}
	for i, name := range []string{"t", "new"} {
		fresh := &Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(902 + i)}, ObjectNamespace: ns.Address(), ObjectName: name, ObjectRevision: 4}
		if err := c.Create(fresh); err != nil {
			t.Fatal(err)
		}
		if c.LookupName(DescriptorNameKey(fresh)) != fresh.Address() {
			t.Fatal("fresh identity did not acquire freed name")
		}
	}
	created := &Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 904}, ObjectNamespace: ns.Address(), ObjectName: "private", ObjectRevision: 5}
	if err := c.Create(created); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"private_middle", "private_new"} {
		previous := DescriptorNameKey(created)
		moved := *created
		moved.ObjectName = name
		c.RenameStagedDescriptor(previous, &moved, DescriptorNameKey(&moved))
		if c.LookupName(previous) != (ids.ObjectAddress{}) || c.LookupName(DescriptorNameKey(&moved)) != moved.Address() {
			t.Fatal("staged rename retained prior name")
		}
		created = &moved
	}
	merged := c.State()
	if merged.Get(rel.Address()) != nil || merged.NameAddress(middleKey) != (ids.ObjectAddress{}) {
		t.Fatal("merged state resurrected removed identity")
	}
	if retained.Get(rel.Address()) != &next || retained.NameAddress(newKey) != rel.Address() || base.Get(rel.Address()) != rel || base.NameAddress(oldKey) != rel.Address() {
		t.Fatal("later candidate changes mutated retained or base state")
	}
}

func TestCandidateDefaultObservations(t *testing.T) {
	base, ns, _ := catalogReadFixture()
	key := DefaultACLKey{Schema: ns.Address()}
	absent := DefaultACLKey{}
	otherKey := DefaultACLKey{Schema: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 999}}
	original, other := &DefaultACLRecord{OID: 10}, &DefaultACLRecord{OID: 11}
	base.defaultACLs = map[DefaultACLKey]*DefaultACLRecord{key: original, otherKey: other}
	c := NewCandidate(Read{state: base})
	if r, ok := c.DefaultObservation(absent); ok || r != nil {
		t.Fatal("unobserved absence is present")
	}
	c.ObserveDefault(absent)
	if r, ok := c.DefaultObservation(absent); !ok || r != nil {
		t.Fatal("observed absence was lost")
	}
	c.ObserveDefault(key)
	c.SetDefaultACL(key, &DefaultACLRecord{OID: 12})
	c.ObserveDefault(key)
	if r, ok := c.DefaultObservation(key); !ok || r != original {
		t.Fatal("observation was replaced by private output")
	}
	c.SetDefaultACL(key, nil)
	if r, ok := c.DefaultACLChange(key); !ok || r != nil {
		t.Fatal("nil delta is not a present tombstone")
	}
	c.SetDefaultACL(otherKey, nil)
	if _, ok := c.DefaultObservation(otherKey); ok {
		t.Fatal("delta-only TEMP cleanup created an observation")
	}
	if !c.DefaultRecordsUnchanged(base) {
		t.Fatal("unchanged original inputs conflict")
	}
	current := CloneState(base)
	current.defaultACLs = map[DefaultACLKey]*DefaultACLRecord{key: &DefaultACLRecord{OID: 10}, otherKey: other}
	if c.DefaultRecordsUnchanged(current) {
		t.Fatal("changed record pointer escaped conflict detection")
	}
	current.defaultACLs[key] = original
	current.defaultACLs[absent] = &DefaultACLRecord{OID: 13}
	if c.DefaultRecordsUnchanged(current) {
		t.Fatal("newly present observed-absent input escaped conflict detection")
	}
	if base.DefaultACL(key) != original || base.DefaultACL(otherKey) != other {
		t.Fatal("private policy changed base")
	}
}

func TestCandidateRetainedState(t *testing.T) {
	base, ns, rel := catalogReadFixture()
	base.operatorNames = &OperatorNameIndex{Buckets: map[OperatorNameKey][]ids.ObjectAddress{}}
	base.procedureNames = &ProcedureNameIndex{Buckets: map[ProcedureNameKey][]ids.ObjectAddress{}, SQLNames: map[string]int{}}
	rel.Index = map[string]int{"id": 0}
	key, untouched := DefaultACLKey{Schema: ns.Address()}, DefaultACLKey{}
	original, shared := &DefaultACLRecord{OID: 10}, &DefaultACLRecord{OID: 11}
	base.defaultACLs = map[DefaultACLKey]*DefaultACLRecord{key: original, untouched: shared}
	c := NewCandidate(Read{state: base})
	first := *rel
	first.ObjectName = "first"
	first.ObjectRevision = 2
	if err := c.Replace(&first); err != nil {
		t.Fatal(err)
	}
	firstACL := &DefaultACLRecord{OID: 12}
	c.SetDefaultACL(key, firstACL)
	retained := c.State()
	read := Read{state: retained}
	second := first
	second.ObjectName = "second"
	second.ObjectRevision = 3
	if err := c.Replace(&second); err != nil {
		t.Fatal(err)
	}
	c.SetDefaultACL(key, nil)
	if read.Get(rel.Address()) != &first || read.NameAddress(DescriptorNameKey(&first)) != rel.Address() || retained.DefaultACL(key) != firstACL || retained.DefaultACL(untouched) != shared || read.Epoch() != 1 {
		t.Fatal("retained state lost prior descriptor/name/ACL/metadata")
	}
	if base.Get(rel.Address()) != rel || base.DefaultACL(key) != original || base.NameAddress(DescriptorNameKey(rel)) != rel.Address() {
		t.Fatal("base changed")
	}
	if retained.Get(ns.Address()) != ns || retained.MustRelation(rel.Address()).Index["id"] != 0 {
		t.Fatal("untouched descriptor or payload lost")
	}
	if retained.operatorNames != base.operatorNames || retained.procedureNames != base.procedureNames {
		t.Fatal("metadata-only relation edit detached unchanged indexes")
	}
}
