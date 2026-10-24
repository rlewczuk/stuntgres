package catalog

import (
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestOperatorClassDefaultMultiplicity(t *testing.T) {
	state := CloneState(familyCatalogFixture().State())
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int4 {
			d := &Type{SQLType: typ, ObjectRevision: 1}
			state.SetDescriptor(d.Address(), d)
			break
		}
	}
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}
	method := &AccessMethod{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: 97000}, ObjectName: "multiplicity_method", ObjectRevision: 1, Native: BtreeAccessMethod, Handler: 330}
	family := &OperatorFamily{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: 97001}, ObjectNamespace: ns, ObjectName: "multiplicity_family", ObjectRevision: 1, Method: 97000}
	for _, d := range []Descriptor{method, family} {
		state.SetDescriptor(d.Address(), d)
		state.BindName(DescriptorNameKey(d), d.Address())
	}
	base := NewRead(nil, state)
	first := &OperatorClass{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassCatalogID, ObjectID: 97002}, ObjectNamespace: ns, ObjectName: "z_default", ObjectRevision: 1, Method: 97000, Family: 97001, InputType: 23, Default: true}
	second := *first
	second.ObjectAddress.ObjectID = 97003
	second.ObjectName = "a_default"
	a, b := NewCandidate(base), NewCandidate(base)
	if err := a.Create(first); err != nil {
		t.Fatal(err)
	}
	if err := b.Create(&second); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []*Candidate{&a, &b} {
		count := 0
		for key := range candidate.Touched() {
			count++
			if key.ClassID != ids.OperatorClassCatalogID || key.Signature != "97000" {
				t.Fatal("non-name default precondition", key)
			}
		}
		if count != 1 {
			t.Fatal("default absence became a name fence", count)
		}
		read := NewRead(nil, candidate.State())
		if _, ok, err := read.DefaultIndexClassID(97000, sqltype.Int4); !ok || err != nil {
			t.Fatal("single private default", ok, err)
		}
	}
	if _, ok, err := base.DefaultIndexClassID(97000, sqltype.Int4); ok || err != nil {
		t.Fatal("candidate changed captured root", ok, err)
	}
	checkMultiple := func(s *State) {
		t.Helper()
		read := NewRead(nil, s)
		bucket, _ := s.defaultClasses.Get(defaultClassPair{97000, 23})
		if !slices.Equal(bucket, []ids.OID{97003, 97002}) {
			t.Fatal("default diagnostic order", bucket)
		}
		if chosen, ok := read.AnyDefaultOperatorClass(97000, 23); !ok || chosen != &second {
			t.Fatal("sequential presence/order", chosen)
		}
		if _, ok := read.DefaultOperatorClass(97000, 23); ok {
			t.Fatal("metadata chose arbitrary winner")
		}
		typ, _ := read.LookupType(sqltype.Int4)
		if _, ok := read.UniqueDefaultIndexClass(97000, typ); ok {
			t.Fatal("optional proof chose arbitrary winner")
		}
		_, ok, err := read.DefaultIndexClass(97000, typ)
		var diagnostic *sqlstate.Error
		if ok || !errors.As(err, &diagnostic) || diagnostic.Code != "42710" || diagnostic.Message != "there are multiple default operator classes for data type integer" || diagnostic.Detail != "" || diagnostic.Hint != "" {
			t.Fatal("required resolver lost ambiguity", ok, err)
		}
		for _, class := range []*OperatorClass{first, &second} {
			if err := (OperatorClassProvider{}).Validate(read, class); err != nil {
				t.Fatal("valid nonunique default provider", err)
			}
			bound, ok := read.IndexClassForMethod(class.Address().ObjectID, method)
			if !ok || bound.OID() != uint32(class.Address().ObjectID) || bound.MethodOID() != 97000 || bound.FamilyOID() != 97001 {
				t.Fatal("named class consulted ambiguous default", bound)
			}
		}
	}
	var published *State
	for _, order := range [][2]*Candidate{{&a, &b}, {&b, &a}} {
		one := order[0].ApplyTo(state)
		both := order[1].ApplyTo(one)
		checkMultiple(both)
		if one.defaultClassCountForTest(97000, 23) != 1 {
			t.Fatal("publication mutated predecessor")
		}
		published = both
	}
	// Projecting a combined candidate and applying to current use the same derived
	// authority; no commit-order winner or fake default-name slot exists.
	joined := NewCandidate(base)
	if err := joined.Create(&second); err != nil {
		t.Fatal(err)
	}
	if err := joined.Create(first); err != nil {
		t.Fatal(err)
	}
	checkMultiple(joined.State())
	clone := CloneState(published)
	captured, _ := published.defaultClasses.Get(defaultClassPair{97000, 23})
	other := *family
	other.ObjectRevision++
	clone.SetDescriptor(other.Address(), &other)
	unchanged, _ := clone.defaultClasses.Get(defaultClassPair{97000, 23})
	if &captured[0] != &unchanged[0] {
		t.Fatal("unrelated descriptor copied default bucket")
	}
	renamed := *first
	renamed.ObjectName = "0_default"
	renamed.ObjectRevision++
	clone.SetDescriptor(renamed.Address(), &renamed)
	ordered, _ := clone.defaultClasses.Get(defaultClassPair{97000, 23})
	if !slices.Equal(ordered, []ids.OID{97002, 97003}) {
		t.Fatal("rename did not reorder bucket", ordered)
	}
	if !slices.Equal(captured, []ids.OID{97003, 97002}) {
		t.Fatal("rename mutated retained bucket", captured)
	}
	moved := second
	moved.InputType = 25
	moved.ObjectRevision++
	clone.SetDescriptor(moved.Address(), &moved)
	if clone.defaultClassCountForTest(97000, 23) != 1 || clone.defaultClassCountForTest(97000, 25) != 1 {
		t.Fatal("pair replacement left stale entry")
	}
	beforeClear := CloneState(clone)
	cleared := moved
	cleared.Default = false
	cleared.ObjectRevision++
	clone.SetDescriptor(cleared.Address(), &cleared)
	if clone.defaultClassCountForTest(97000, 25) != 0 {
		t.Fatal("clearing default retained bucket")
	}
	if !moved.Default || beforeClear.Get(moved.Address()) != &moved || beforeClear.defaultClassCountForTest(97000, 25) != 1 {
		t.Fatal("clearing default mutated its immutable predecessor")
	}
	clone.SetDescriptor(second.Address(), &second)
	checkMultiple(published)
	for _, removed := range []*OperatorClass{first, &second} {
		candidate := NewCandidate(NewRead(nil, published))
		candidate.RecordRemoval(ids.ObjectRef{Address: removed.Address()}, removed)
		one := candidate.State()
		remaining, ok, err := NewRead(nil, one).DefaultIndexClassID(97000, sqltype.Int4)
		if !ok || err != nil || remaining.OID() == uint32(removed.Address().ObjectID) {
			t.Fatal("drop-one erased or reselected wrong default", remaining, err)
		}
		one.RemoveDescriptor(ids.ObjectAddress{ClassID: ids.OperatorClassCatalogID, ObjectID: ids.OID(remaining.OID())})
		if _, ok, err := NewRead(nil, one).DefaultIndexClassID(97000, sqltype.Int4); ok || err != nil {
			t.Fatal("last drop retained default", ok, err)
		}
	}
	rebuilt := CloneState(published)
	previous := rebuilt.ResetMaps()
	if rebuilt.defaultClassCountForTest(97000, 23) != 0 {
		t.Fatal("reset maps retained derived index")
	}
	rebuilt.BeginBulkLoad(256)
	for address, d := range previous {
		rebuilt.SetDescriptor(address, d)
		rebuilt.BindName(DescriptorNameKey(d), address)
	}
	rebuilt.EndBulkLoad()
	checkMultiple(rebuilt)
	rebuilt.ResetDescriptors()
	if rebuilt.defaultClassCountForTest(97000, 23) != 0 {
		t.Fatal("reset descriptors retained derived index")
	}
	remapped := NewState(2)
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{method.Address(): {ClassID: ids.AccessMethodClassID, ObjectID: 98000}, family.Address(): {ClassID: ids.OperatorFamilyClassID, ObjectID: 98001}, first.Address(): {ClassID: ids.OperatorClassCatalogID, ObjectID: 98002}, second.Address(): {ClassID: ids.OperatorClassCatalogID, ObjectID: 98003}}
	remapped.BeginBulkLoad(2)
	for _, class := range []*OperatorClass{first, &second} {
		d := RemapOperatorClass(class, mapping)
		remapped.SetDescriptor(d.Address(), d)
	}
	remapped.EndBulkLoad()
	bucket, _ := remapped.defaultClasses.Get(defaultClassPair{98000, 23})
	if !slices.Equal(bucket, []ids.OID{98003, 98002}) || remapped.defaultClassCountForTest(97000, 23) != 0 {
		t.Fatal("remap did not rebuild exact method bucket", bucket)
	}
	checkMultiple(published)
}

func (s *State) defaultClassCountForTest(method, input ids.OID) int {
	bucket, _ := s.defaultClasses.Get(defaultClassPair{method, input})
	return len(bucket)
}
