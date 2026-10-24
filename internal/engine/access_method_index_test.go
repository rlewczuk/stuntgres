package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func accessMethodClassUnit(t *testing.T, e *Engine, name string) (*catalog.AccessMethod, *catalog.OperatorFamily, *catalog.OperatorClass, *catalog.OperatorFamilySupport) {
	t.Helper()
	method := accessMethodUnit(t, e, name, catalog.BtreeAccessMethod, 330)
	family, class, member := operatorClassUnitDefinitions(t, e, name, 351)
	family.Method, class.Method = method.Address().ObjectID, method.Address().ObjectID
	class.Default = true
	accessMethodPublish(t, e, method, family, class, member)
	return method, family, class, member
}
func TestAccessMethodIndexRouting(t *testing.T) {
	e := New()
	first, family, class, member := accessMethodClassUnit(t, e, "am_root")
	second, secondFamily, secondClass, _ := accessMethodClassUnit(t, e, "am_root_other")
	view := e.CatalogRead()
	for _, tc := range []struct {
		method *catalog.AccessMethod
		family *catalog.OperatorFamily
		class  *catalog.OperatorClass
	}{{first, family, class}, {second, secondFamily, secondClass}} {
		selected, ok, err := view.DefaultIndexClassID(tc.method.Address().ObjectID, sqltype.Int4)
		if err != nil {
			t.Fatal(err)
		}
		if !ok || selected.OID() != uint32(tc.class.Address().ObjectID) || selected.FamilyOID() != uint32(tc.family.Address().ObjectID) {
			t.Fatal("cross-method default", selected)
		}
	}
	typ, _ := view.LookupType(sqltype.Int4)
	val := func(n int64) sqltype.Value {
		v, err := sqltype.Integer(typ, n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	selected, ok := view.IndexClass(class.Address().ObjectID)
	if !ok {
		t.Fatal("missing actual class")
	}
	selected, err := routine.BindClassSupport(view, selected)
	if err != nil {
		t.Fatal(err)
	}
	support := selected.Support(1)
	if support.Routine != 351 || support.Member != uint32(member.Address().ObjectID) || support.Family != uint32(family.Address().ObjectID) {
		t.Fatal("wrong support capture", support)
	}
	parent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: catalogUnitOID(t, e)}, ObjectRevision: 1, RelationKind: catalog.TableRelation, Attributes: []int32{1}, Columns: []catalog.Column{{Name: "n", Usage: typ.Usage()}}, Index: map[string]int{"n": 0}}
	definition := &catalog.IndexDefinition{Method: catalog.BtreeMethod, MethodOID: first.Address().ObjectID, Parent: parent.Address(), Keys: []catalog.IndexKey{{Attribute: 1, Class: selected}}, Ready: true, Valid: true, Live: true}
	idx := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: catalogUnitOID(t, e)}, ObjectRevision: 1, SQLIndex: definition}
	values := []sqltype.Value{val(2), val(1), val(2), sqltype.Null(typ)}
	store := &storage.Relation{}
	for i, v := range values {
		store.Versions = append(store.Versions, storage.RowVersion{Row: ids.RowID(i + 1), Version: ids.VersionID(i + 1), Born: 1, Values: []sqltype.Value{v}})
	}
	eval := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	root, err := index.BuildIndexRoot(eval, parent, definition, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	identities := func(r *storage.IndexRoot) []ids.RowID {
		var out []ids.RowID
		for _, entry := range r.Entries {
			if entry.Version != ids.VersionID(entry.Row) {
				t.Fatal("lost live version", entry)
			}
			out = append(out, entry.Row)
		}
		return out
	}
	if got := identities(root); !reflect.DeepEqual(got, []ids.RowID{2, 1, 3, 4}) {
		t.Fatal("built root", got)
	}
	writes := []storage.RowVersion{{Row: 5, Version: 5, Values: []sqltype.Value{val(0)}}, {Row: 6, Version: 6, Values: []sqltype.Value{val(2)}}, {Row: 7, Version: 7, Values: []sqltype.Value{sqltype.Null(typ)}}}
	nextVersions := append(append([]storage.RowVersion(nil), store.Versions...), writes...)
	delta := &index.RowPublication{Store: store, Root: store.Versions, Next: nextVersions, Writes: writes, SingleSlot: -1}
	next, err := index.MergeIndexRoot(eval, parent, idx, root.Entries, delta, false, true, false, "", sqltype.OutputEnvironment{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := identities(next); !reflect.DeepEqual(got, []ids.RowID{5, 2, 1, 3, 6, 4, 7}) {
		t.Fatal("maintained root", got)
	}
	if got := identities(root); !reflect.DeepEqual(got, []ids.RowID{2, 1, 3, 4}) {
		t.Fatal("old root mutated", got)
	}
	if definition.MethodIdentity() != first.Address().ObjectID || definition.Keys[0].Class.Support(1) != support {
		t.Fatal("maintenance lost exact identity/support")
	}
	crossed := *definition
	crossed.MethodOID = second.Address().ObjectID
	if _, err := index.BindIndexOrdering(view, parent, &crossed); err == nil {
		t.Fatal("crossed method/class accepted")
	}
	empty := *class
	empty.ObjectAddress.ObjectID = catalogUnitOID(t, e)
	empty.ObjectName += "_empty"
	empty.Family = secondFamily.Address().ObjectID
	state := catalog.CloneState(view.State())
	state.SetDescriptor(empty.Address(), &empty)
	state.BindName(catalog.DescriptorNameKey(&empty), empty.Address())
	if _, ok := view.WithState(state).IndexClass(empty.Address().ObjectID); ok {
		t.Fatal("crossed method/family accepted")
	}
	without := catalog.CloneState(view.State())
	without.RemoveDescriptor(member.Address())
	if _, err := routine.BindClassSupport(view.WithState(without), selected); err == nil {
		t.Fatal("missing actual member reused captured/builtin support")
	}
}
