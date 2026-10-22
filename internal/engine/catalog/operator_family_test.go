package catalog

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"sync"
	"testing"
)

func familyCatalogFixture() Read {
	state := NewState(1)
	namespace := &Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, ObjectName: "pg_catalog", ObjectRevision: 1}
	state.SetDescriptor(namespace.Address(), namespace)
	state.BindName(DescriptorNameKey(namespace), namespace.Address())
	InstallBuiltinFamilies(state, authorization.PrincipalRef{})
	return NewRead(nil, state)
}
func TestOperatorFamilyCatalog(t *testing.T) {
	view := familyCatalogFixture()
	t.Run("dependency classification", func(t *testing.T) { operatorFamilyDependencyCases(t, view) })
	if view.DescriptorCount() != 1+49+292+185 {
		t.Fatal("incomplete released family inventory", view.DescriptorCount())
	}
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}
	btree := view.NameAddress(FamilyNameKey(ns, 403, "integer_ops"))
	hash := view.NameAddress(FamilyNameKey(ns, 405, "integer_ops"))
	if btree.ObjectID != 1976 || hash.ObjectID != 1977 || btree == hash {
		t.Fatal("family method identity", btree, hash)
	}
	support, ok := view.FamilySupport(1976, 23, 23, 1)
	if !ok || support.Function != 351 || support.Left != 23 || support.Right != 23 || support.OwningClass != 1978 {
		t.Fatal("released comparator member", support)
	}
	hashSupport, ok := view.FamilySupport(1977, 23, 23, 1)
	if !ok || hashSupport.Function != 450 {
		t.Fatal("released hash member", hashSupport)
	}
	for number, operator := range []ids.OID{97, 523, 96, 525, 521} {
		member, ok := view.FamilyOperator(1976, 23, 23, int16(number+1), 's')
		if !ok || member.Operator != operator || member.Method != 403 || member.SortFamily != 0 {
			t.Fatal("released strategy", number, member)
		}
	}
	if _, ok := view.FamilyOperator(1976, 23, 23, 1, 'o'); ok {
		t.Fatal("ordering purpose aliases search member")
	}
	cross, ok := view.FamilyOperator(1976, 21, 20, 1, 's')
	if !ok || cross.Operator != 1864 || cross.OwningClass != 0 {
		t.Fatal("released cross-type metadata", cross)
	}
	optional, ok := view.FamilySupport(1976, 23, 23, 2)
	if !ok || optional.Function != 3130 || optional.OwningClass != 0 || !OperatorFamilySeeded(optional) {
		t.Fatal("original optional pin authority", optional)
	}
	for _, d := range view.Descriptors() {
		if _, ok := d.(*Namespace); ok {
			continue
		}
		if err := (OperatorFamilyProvider{Class: d.Address().ClassID}).Validate(view, d); err != nil {
			t.Fatal(d, err)
		}
	}
	loose := *support
	loose.ObjectAddress.ObjectID = 90001
	loose.OwningClass = 0
	deps := OperatorFamilyDependencies(view, &loose)
	if OperatorFamilySeeded(&loose) || deps[0].Kind != 'a' || deps[0].Address != btree || deps[1].Kind != 'a' {
		t.Fatal("loose dependency authority", deps)
	}
	hard := OperatorFamilyDependencies(view, support)
	if hard[0].Kind != 'i' || hard[0].Address.ClassID != 2616 || hard[0].Address.ObjectID != 1978 || hard[1].Kind != 'n' {
		t.Fatal("hard insertion dependency", hard)
	}
}

func TestOperatorFamilyPublication(t *testing.T) {
	base := familyCatalogFixture()
	original, _ := base.FamilySupport(1976, 23, 23, 1)
	candidate := NewCandidate(base)
	custom := &OperatorFamily{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: 90001}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, ObjectRevision: 2, ObjectName: "publication_family", Method: 403}
	if err := candidate.Create(custom); err != nil {
		t.Fatal(err)
	}
	member := &OperatorFamilySupport{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodProcedureClassID, ObjectID: 90002}, ObjectRevision: 3, Family: 90001, Left: 23, Right: 23, Number: 1, Function: 351}
	if err := candidate.Create(member); err != nil {
		t.Fatal(err)
	}
	retained := base.WithState(candidate.State())
	duplicate := *member
	duplicate.ObjectAddress.ObjectID = 90003
	if err := candidate.Create(&duplicate); err == nil {
		t.Fatal("duplicate member accepted")
	}
	strategy, _ := base.FamilyOperator(1976, 23, 23, 1, 's')
	search := *strategy
	search.ObjectAddress.ObjectID = 90005
	search.Family = 90001
	search.OwningClass = 0
	if err := candidate.Create(&search); err != nil {
		t.Fatal(err)
	}
	ordering := search
	ordering.ObjectAddress.ObjectID = 90006
	ordering.Purpose = 'o'
	ordering.SortFamily = 1976
	if err := candidate.Create(&ordering); err == nil {
		t.Fatal("purpose distinguished a primary strategy slot")
	}
	if _, ok := base.FamilySupport(90001, 23, 23, 1); ok {
		t.Fatal("private publication escaped")
	}
	if got, _ := base.FamilySupport(1976, 23, 23, 1); got != original {
		t.Fatal("unrelated builtin provider changed")
	}
	edit := NewCandidate(retained)
	edit.RecordRemoval(ids.ObjectRef{Address: member.Address(), Epoch: 1}, member)
	removed := retained.WithState(edit.State())
	if _, ok := removed.FamilySupport(90001, 23, 23, 1); ok {
		t.Fatal("removed member still selected")
	}
	if got, _ := retained.FamilySupport(90001, 23, 23, 1); got != member {
		t.Fatal("retained snapshot mutated")
	}
	recreated := *member
	recreated.ObjectAddress.ObjectID = 90004
	recreated.ObjectRevision = 4
	recreated.Function = 181
	replacement := NewCandidate(removed)
	if err := replacement.Create(&recreated); err != nil {
		t.Fatal(err)
	}
	current := removed.WithState(replacement.State())
	if got, _ := current.FamilySupport(90001, 23, 23, 1); got != &recreated || got.Address() == member.Address() {
		t.Fatal("readd failed to mint identity")
	}
	var readers sync.WaitGroup
	for _, v := range []Read{base, retained, removed, current} {
		readers.Add(1)
		go func(v Read) {
			defer readers.Done()
			for range 100 {
				got, ok := v.FamilySupport(1976, 23, 23, 1)
				if !ok || got != original {
					t.Error("immutable unrelated lookup changed")
				}
			}
		}(v)
	}
	readers.Wait()
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{custom.Address(): {ClassID: ids.OperatorFamilyClassID, ObjectID: 91001}, member.Address(): {ClassID: ids.AccessMethodProcedureClassID, ObjectID: 91002}}
	imported := RemapOperatorFamily(member, mapping).(*OperatorFamilySupport)
	if imported == member || imported.Family != 91001 || imported.Address().ObjectID != 91002 || member.Family != 90001 || member.Address().ObjectID != 90002 {
		t.Fatal("source identity adopted during remap", imported, member)
	}
}
