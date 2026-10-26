package catalog

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestOperatorClassCatalogMethodIdentity(t *testing.T) {
	// Both descriptors use the same live authority as builtin methods.
	facts := []*AccessMethod{
		{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: 403}, ObjectName: "btree", ObjectRevision: 1, Native: BtreeAccessMethod, Handler: 330},
		{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: 90000}, ObjectName: "other_btree", ObjectRevision: 1, Native: BtreeAccessMethod, Handler: 330},
	}
	state := NewState(1)
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}
	namespace := &Namespace{ObjectAddress: ns, ObjectName: "pg_catalog", ObjectRevision: 1}
	state.SetDescriptor(ns, namespace)
	state.BindName(DescriptorNameKey(namespace), ns)
	candidate := NewCandidate(NewRead(nil, state))
	for i, fact := range facts {
		if err := candidate.Create(fact); err != nil {
			t.Fatal(err)
		}
		family := &OperatorFamily{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: ids.OID(91000 + i)}, ObjectNamespace: ns, ObjectName: "same_family", ObjectRevision: 1, Method: fact.Address().ObjectID}
		class := &OperatorClass{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassCatalogID, ObjectID: ids.OID(92000 + i)}, ObjectNamespace: ns, ObjectName: "same_class", ObjectRevision: 1, Method: fact.Address().ObjectID, Family: family.Address().ObjectID, InputType: 23, Default: true}
		for _, d := range []Descriptor{family, class} {
			if err := candidate.Create(d); err != nil {
				t.Fatal(err)
			}
		}
	}
	read := NewRead(nil, candidate.State())
	var retained [2]IndexDefinition
	for i, fact := range facts {
		class, ok := read.LookupOperatorClass(ns, fact.Address().ObjectID, "same_class")
		if !ok || class.Method != fact.Address().ObjectID {
			t.Fatal("name lost exact method", class)
		}
		def, ok := read.DefaultOperatorClass(fact.Address().ObjectID, 23)
		if !ok || def != class {
			t.Fatal("default lost exact method", def)
		}
		c, ok := read.IndexClassForMethod(class.Address().ObjectID, fact)
		if !ok || c.MethodOID() != uint32(fact.Address().ObjectID) || c.FamilyOID() != uint32(class.Family) {
			t.Fatal("construction lost exact identity", c)
		}
		retained[i] = IndexDefinition{Method: BtreeMethod, MethodOID: fact.Address().ObjectID, Keys: []IndexKey{{Class: c}}}
	}
	if retained[0].Method != retained[1].Method || retained[0].MethodIdentity() == retained[1].MethodIdentity() || retained[0].Keys[0].Class.SameInferenceIdentity(retained[1].Keys[0].Class) {
		t.Fatal("physical implementation collapsed exact identity")
	}
	// A class cannot borrow the other method's family even though both execute
	// the same physical algorithm and admit the same datum type.
	bad := *read.OperatorClasses()[0]
	bad.Family = 91001
	crossed := CloneState(read.State())
	crossed.SetDescriptor(bad.Address(), &bad)
	if _, ok := NewRead(nil, crossed).IndexClassForMethod(bad.Address().ObjectID, facts[0]); ok {
		t.Fatal("cross-method family accepted")
	}
	if retained[0].MethodIdentity() != 403 || retained[0].Keys[0].Class.MethodOID() != 403 {
		t.Fatal("retained definition changed")
	}
}

func TestOperatorClassCatalogDefaults(t *testing.T) {
	state := CloneState(familyCatalogFixture().State())
	base := NewRead(nil, state)
	if len(base.OperatorClasses()) != 145 {
		t.Fatal("incomplete seeds", len(base.OperatorClasses()))
	}
	original, ok := base.DefaultOperatorClass(403, 23)
	if !ok || original.Address().ObjectID != 1978 {
		t.Fatal("builtin default", original)
	}
	candidate := NewCandidate(base)
	conflict := *original
	conflict.ObjectAddress.ObjectID = 95000
	conflict.ObjectName = "other_default"
	if err := candidate.Create(&conflict); err != nil {
		t.Fatal(err)
	}
	if candidate.DescriptorCount() != 1 {
		t.Fatal("descriptor not retained")
	}
	if _, ok := NewRead(nil, candidate.State()).DefaultOperatorClass(403, 23); ok {
		t.Fatal("multiple defaults selected a winner")
	}
	candidate = NewCandidate(base)
	renamed := *original
	renamed.ObjectName = "current_default"
	renamed.ObjectRevision++
	if err := candidate.Replace(&renamed); err != nil {
		t.Fatal(err)
	}
	current := NewRead(nil, candidate.State())
	if _, ok := current.LookupOperatorClass(original.Namespace(), 403, "int4_ops"); ok {
		t.Fatal("stale class name")
	}
	if got, ok := current.DefaultOperatorClass(403, 23); !ok || got != &renamed {
		t.Fatal("rename lost default", got)
	}
	if got, ok := base.DefaultOperatorClass(403, 23); !ok || got != original {
		t.Fatal("base changed")
	}
	candidate.RecordRemoval(ids.ObjectRef{Address: renamed.Address()}, &renamed)
	removed := NewRead(nil, candidate.State())
	if _, ok := removed.DefaultOperatorClass(403, 23); ok {
		t.Fatal("removed default retained")
	}
	if _, ok, err := removed.DefaultIndexClassID(403, sqltype.Int4); ok || err != nil {
		t.Fatal("static builtin default fallback")
	}
}

func TestOperatorClassIdentityRemap(t *testing.T) {
	class := &OperatorClass{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassCatalogID, ObjectID: 90001}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 90002}, ObjectName: "remapped", ObjectRevision: 1, Method: 403, Family: 90003, InputType: 90004, StorageType: 90005}
	family := &OperatorFamily{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: 90003}, Method: 403}
	member := &OperatorFamilyOperator{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodOperatorClassID, ObjectID: 90006}, Family: 90003, Method: 403, InternalOwner: class.Address()}
	support := &OperatorFamilySupport{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodProcedureClassID, ObjectID: 90007}, Family: 90003, InternalOwner: class.Address()}
	mapping := make(map[ids.ObjectAddress]ids.ObjectAddress)
	for _, address := range []ids.ObjectAddress{class.Address(), class.Namespace(), family.Address(), member.Address(), support.Address(), {ClassID: 2601, ObjectID: 403}, {ClassID: ids.TypeClassID, ObjectID: 90004}, {ClassID: ids.TypeClassID, ObjectID: 90005}} {
		next := address
		next.ObjectID += 100
		mapping[address] = next
	}
	got := RemapOperatorClass(class, mapping)
	if got == class || got.Method != 503 || got.Family != 90103 || got.InputType != 90104 || got.StorageType != 90105 || got.Address().ObjectID != 90101 || got.Namespace().ObjectID != 90102 || class.Method != 403 {
		t.Fatal("class exact identity remap", got)
	}
	if RemapOperatorClass(class, nil) != class {
		t.Fatal("unchanged class copied")
	}
	if got := RemapOperatorFamily(family, mapping).(*OperatorFamily); got.Method != 503 || family.Method != 403 {
		t.Fatal("family method remap", got)
	}
	if got := RemapOperatorFamily(member, mapping).(*OperatorFamilyOperator); got.Method != 503 || got.InternalOwner != mapping[class.Address()] || member.InternalOwner != class.Address() {
		t.Fatal("operator method/class remap", got)
	}
	if got := RemapOperatorFamily(support, mapping).(*OperatorFamilySupport); got.InternalOwner != mapping[class.Address()] || support.InternalOwner != class.Address() {
		t.Fatal("support class remap", got)
	}
}

func TestOperatorClassValueIdentity(t *testing.T) {
	def := sqltype.IndexClassDefinition{OID: 90001, Method: 403, Family: 90002, Input: 23, Name: "original", FamilyName: "original_family"}
	original := sqltype.NewIndexClass(def)
	renamed := original
	renamed.Name, renamed.Family = "renamed", "renamed_family"
	renamed = renamed.WithSupport(1, sqltype.IndexSupport{Member: 90003, Family: 90002, Routine: 351, Left: 23, Right: 23})
	if !original.SameIdentity(renamed) {
		t.Fatal("presentation/support changed declared identity")
	}
	def.Method = 90004
	if original.SameIdentity(sqltype.NewIndexClass(def)) {
		t.Fatal("method identity ignored")
	}
	def.Method, def.Family = 403, 90005
	if original.SameIdentity(sqltype.NewIndexClass(def)) {
		t.Fatal("family identity ignored")
	}
}
