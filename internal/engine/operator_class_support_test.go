package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
	"testing"
)

// This fixture constructs real descriptors and real registered routine bindings;
// no SQL, name-based class cloning or callback stands in for native support.
func operatorClassUnitDefinitions(t *testing.T, e *Engine, name string, function ids.OID) (*catalog.OperatorFamily, *catalog.OperatorClass, *catalog.OperatorFamilySupport) {
	t.Helper()
	builtin, ok := e.CatalogRead().OperatorClass(1978)
	if !ok {
		t.Fatal("missing bootstrap class authority")
	}
	family := &catalog.OperatorFamily{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: catalogUnitOID(t, e)}, ObjectNamespace: builtin.Namespace(), ObjectRevision: 1, ObjectName: name + "_family", Method: 403, Owner: builtin.Owner}
	class := &catalog.OperatorClass{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassCatalogID, ObjectID: catalogUnitOID(t, e)}, ObjectNamespace: builtin.Namespace(), ObjectRevision: 1, ObjectName: name, Method: 403, Family: family.Address().ObjectID, InputType: 23, Owner: builtin.Owner}
	member := &catalog.OperatorFamilySupport{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodProcedureClassID, ObjectID: catalogUnitOID(t, e)}, ObjectRevision: 1, Family: family.Address().ObjectID, Left: 23, Right: 23, Number: 1, Function: function, InternalOwner: class.Address()}
	return family, class, member
}

func TestOperatorClassSupportExecution(t *testing.T) {
	e := New()
	base := e.CatalogRead()
	typ, _ := base.LookupType(sqltype.Int4)
	ctx := context.Background()
	value := func(n int64) sqltype.Value {
		v, err := sqltype.Integer(typ, n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	rows := func(root *storage.IndexRoot) []ids.RowID {
		var result []ids.RowID
		for _, entry := range root.Entries {
			result = append(result, entry.Row)
			if entry.Version != ids.VersionID(entry.Row) {
				t.Fatal("lost row version", entry)
			}
		}
		return result
	}
	for _, tc := range []struct {
		name    string
		routine ids.OID
		want    int
	}{{"class_compare", 351, -1}, {"class_subtract", 181, -2}} {
		t.Run(tc.name, func(t *testing.T) {
			family, class, member := operatorClassUnitDefinitions(t, e, tc.name, tc.routine)
			candidate := catalog.NewCandidate(base)
			for _, d := range []catalog.Descriptor{family, class, member} {
				if err := candidate.Create(d); err != nil {
					t.Fatal(err)
				}
			}
			view := base.WithState(candidate.State())
			selected, ok := view.IndexClass(class.Address().ObjectID)
			if !ok {
				t.Fatal("custom class unavailable")
			}
			parent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: catalogUnitOID(t, e)}, ObjectRevision: 1, RelationKind: catalog.TableRelation, Attributes: []int32{1}, Columns: []catalog.Column{{Name: "n", Usage: typ.Usage()}}, Index: map[string]int{"n": 0}}
			definition := &catalog.IndexDefinition{Method: catalog.BtreeMethod, MethodOID: 403, Parent: parent.Address(), Keys: []catalog.IndexKey{{Attribute: 1, Class: selected, ExplicitClass: true}}, Ready: true, Valid: true, Live: true}
			idx := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: catalogUnitOID(t, e)}, ObjectRevision: 1, SQLIndex: definition}
			ordering, err := index.BindIndexOrdering(view, parent, definition)
			if err != nil {
				t.Fatal(err)
			}
			support := ordering.Comparisons[0].Support()
			if selected.OID() != uint32(class.Address().ObjectID) || selected.MethodOID() != 403 || support.Family != uint32(family.Address().ObjectID) || support.Member != uint32(member.Address().ObjectID) || support.Routine != uint32(tc.routine) || support.Left != 23 || support.Right != 23 {
				t.Fatal("wrong custom support identity", selected, support)
			}
			got, err := ordering.Comparisons[0].Compare(ctx, value(1), value(3), sqltype.InputEnvironment{})
			if err != nil || got != tc.want {
				t.Fatal("selected comparator not executed", got, err)
			}
			store := &storage.Relation{Versions: []storage.RowVersion{{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{value(2)}}, {Row: 2, Version: 2, Born: 1, Values: []sqltype.Value{value(1)}}, {Row: 3, Version: 3, Born: 1, Values: []sqltype.Value{sqltype.Null(typ)}}, {Row: 4, Version: 4, Born: 1, Values: []sqltype.Value{value(2)}}}}
			evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Catalog: view}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
			root, err := index.BuildIndexRoot(evaluation, parent, definition, store, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(rows(root), []ids.RowID{2, 1, 4, 3}) {
				t.Fatal("custom built root", rows(root))
			}
			added := storage.RowVersion{Row: 5, Version: 5, Values: []sqltype.Value{value(0)}}
			delta := &index.RowPublication{Store: store, Root: store.Versions, Next: append(append([]storage.RowVersion(nil), store.Versions...), added), Writes: []storage.RowVersion{added}, SingleSlot: -1}
			next, err := index.MergeIndexRoot(evaluation, parent, idx, root.Entries, delta, false, true, false, "", sqltype.OutputEnvironment{}, nil, nil)
			if err != nil || !reflect.DeepEqual(rows(next), []ids.RowID{5, 2, 1, 4, 3}) {
				t.Fatal("custom maintained root", next, err)
			}
			if !reflect.DeepEqual(rows(root), []ids.RowID{2, 1, 4, 3}) {
				t.Fatal("retained root mutated")
			}
			crossed := *class
			crossed.Family = 1976
			crossed.ObjectRevision++
			broken := catalog.NewCandidate(view)
			if err := broken.Replace(&crossed); err != nil {
				t.Fatal(err)
			}
			if _, err := routine.BindClassSupport(view.WithState(broken.State()), selected); err == nil {
				t.Fatal("crossed family capture accepted")
			}
			missing := catalog.NewCandidate(view)
			// A genuinely incomplete current family cannot select static int4 support.
			empty := *family
			empty.ObjectAddress.ObjectID = catalogUnitOID(t, e)
			empty.ObjectName += "_empty"
			incomplete := *class
			incomplete.ObjectAddress.ObjectID = catalogUnitOID(t, e)
			incomplete.ObjectName += "_empty"
			incomplete.Family = empty.Address().ObjectID
			for _, d := range []catalog.Descriptor{&empty, &incomplete} {
				if err := missing.Create(d); err != nil {
					t.Fatal(err)
				}
			}
			missingView := view.WithState(missing.State())
			emptyClass, ok := missingView.IndexClass(incomplete.Address().ObjectID)
			if !ok {
				t.Fatal("incomplete definition rejected")
			}
			if _, err := routine.BindClassSupport(missingView, emptyClass); err == nil {
				t.Fatal("missing support fell back to builtin")
			}
		})
	}
}
