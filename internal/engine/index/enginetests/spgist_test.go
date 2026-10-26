package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

var spgistOwners = []struct {
	name                 string
	oid                  ids.OID
	input                sqltype.ID
	text                 string
	returnable, distance bool
}{
	{"inet_ops", 10018, sqltype.Inet, "10.0.0.1/24", true, false},
	{"range_ops", 10079, sqltype.Int4Range, "[1,3)", true, false},
	{"box_ops", 10083, sqltype.Box, "(2,2),(0,0)", true, true},
	{"quad_point_ops", 10084, sqltype.Point, "(3,4)", true, true},
	{"kd_point_ops", 10085, sqltype.Point, "(3,4)", true, true},
	{"text_ops", 10086, sqltype.Text, "a ", true, false},
	{"poly_ops", 10087, sqltype.Polygon, "((0,0),(4,0),(0,4))", false, true},
}

func TestSPGiSTClassInventory(t *testing.T) {
	view := engine.New().CatalogRead()
	method, ok := view.AccessMethod(4000)
	if !ok || method.Handler != 334 || method.Native != catalog.SpgistAccessMethod {
		t.Fatal(method)
	}
	count := 0
	for _, d := range view.Descriptors() {
		if c, ok := d.(*catalog.OperatorClass); ok && c.Method == 4000 {
			count++
		}
	}
	if count != 7 {
		t.Fatal("class inventory", count)
	}
	for _, owner := range spgistOwners {
		class, ok := view.IndexClass(owner.oid)
		typ, _ := view.LookupType(owner.input)
		if !ok || !class.IsSPGiST() || class.IsGiST() || !class.Accepts(typ) || class.MethodOID() != 4000 {
			t.Fatal(owner, class)
		}
		selected, found, err := view.DefaultIndexClass(4000, typ)
		if err != nil || !found || owner.name != "kd_point_ops" && !selected.SameIdentity(class) || owner.name == "kd_point_ops" && selected.OID() != 10084 {
			t.Fatal(owner, selected, found, err)
		}
		if _, err := index.BindIndexOrdering(view, &catalog.Relation{Attributes: []int32{1}, Columns: []catalog.Column{{Usage: typ.Usage()}}}, &catalog.IndexDefinition{Method: catalog.SpgistMethod, MethodOID: 4000, Keys: []catalog.IndexKey{{Attribute: 1, Class: class}}}); err == nil {
			t.Fatal("SP-GiST acquired B-tree ordering")
		}
	}
}

func TestSPGiSTClassReconstruction(t *testing.T) {
	view := engine.New().CatalogRead()
	text, _ := view.LookupType(sqltype.Text)
	varchar, _ := view.LookupType(sqltype.VarChar)
	integer, _ := view.LookupType(sqltype.Int4)
	class, _ := view.IndexClass(10086)
	original := catalog.IndexKey{Attribute: 1, Class: class}
	rebound, err := index.ReconstructIndexKey(view, original, text.Usage(), varchar.Usage(), 4000)
	if err != nil || !rebound.Class.SameIdentity(class) || rebound.Class.MethodOID() != 4000 || original.Class != class {
		t.Fatal("compatible reconstruction changed method/class identity or source", rebound, err)
	}
	rejected, err := index.ReconstructIndexKey(view, rebound, varchar.Usage(), integer.Usage(), 4000)
	var diagnostic *sqlstate.Error
	want := sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704",
		Message: `data type integer has no default operator class for access method "spgist"`,
		Hint:    "You must specify an operator class for the index or define a default operator class for the data type.",
	}
	if !errors.As(err, &diagnostic) || *diagnostic != want || rejected.Class != rebound.Class || original.Class != class {
		t.Fatal("missing default did not reject before replacing the captured class", rejected, err)
	}
}

func TestSPGiSTMethodProperties(t *testing.T) {
	view := engine.New().CatalogRead()
	for _, owner := range spgistOwners {
		class, _ := view.IndexClass(owner.oid)
		def := &catalog.IndexDefinition{Method: catalog.SpgistMethod, MethodOID: 4000, Keys: []catalog.IndexKey{{Class: class}}, Include: []int32{2}}
		for _, p := range []struct {
			name string
			want bool
		}{{"returnable", owner.returnable}, {"distance_orderable", owner.distance}, {"orderable", false}, {"search_nulls", true}, {"search_array", false}, {"asc", false}, {"desc", false}, {"nulls_first", false}, {"nulls_last", false}} {
			got, known := index.ColumnCapability(view, def, 1, p.name)
			if !known || got != p.want {
				t.Fatal(owner.name, p.name, got, known)
			}
		}
		if got, known := index.ColumnCapability(view, def, 2, "returnable"); !got || !known {
			t.Fatal("included returnability", got, known)
		}
		if _, known := index.ColumnCapability(view, def, 2, "distance_orderable"); known {
			t.Fatal("included distance")
		}
		if owner.distance {
			changed := catalog.CloneState(view.State())
			for _, d := range view.Descriptors() {
				if m, ok := d.(*catalog.OperatorFamilyOperator); ok && m.Family == ids.OID(class.FamilyOID()) && m.Purpose == 'o' {
					changed.RemoveDescriptor(m.Address())
				}
			}
			got, known := index.ColumnCapability(catalog.NewRead(view.Owner(), changed), def, 1, "distance_orderable")
			if !known || got {
				t.Fatal("removed distance member resurrected")
			}
		}
	}
}

type spgistInterruptContext struct {
	context.Context
	remaining int
}

func (c *spgistInterruptContext) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}

func TestSPGiSTLogicalMembership(t *testing.T) {
	view := engine.New().CatalogRead()
	ctx := context.Background()
	for _, owner := range spgistOwners {
		t.Run(owner.name, func(t *testing.T) {
			typ, _ := view.LookupType(owner.input)
			class, _ := view.IndexClass(owner.oid)
			value, err := typ.Input(ctx, owner.text, -1)
			if err != nil {
				t.Fatal(err)
			}
			parent := &catalog.Relation{Attributes: []int32{1}, Columns: []catalog.Column{{Name: "v", Usage: typ.Usage()}}}
			def := &catalog.IndexDefinition{Method: catalog.SpgistMethod, MethodOID: 4000, Keys: []catalog.IndexKey{{Attribute: 1, Class: class}}, Ready: true, Valid: true, Live: true}
			store := &storage.Relation{Versions: []storage.RowVersion{
				{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{value}},
				{Row: 2, Version: 2, Born: 1, Values: []sqltype.Value{sqltype.Null(typ)}},
				{Row: 3, Version: 3, Born: 1, Values: []sqltype.Value{value}},
			}}
			eval := &exec.Context{EvalStatement: exec.EvalStatement{Catalog: view, Context: ctx}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
			root, err := index.BuildIndexRoot(eval, parent, def, store, nil)
			if err != nil {
				t.Fatal(err)
			}
			want := []storage.IndexEntry{{Row: 1, Version: 1, Position: 0, Keys: []sqltype.Value{value}}, {Row: 2, Version: 2, Position: 1, Keys: []sqltype.Value{sqltype.Null(typ)}}, {Row: 3, Version: 3, Position: 2, Keys: []sqltype.Value{value}}}
			if root.Hash != nil || !reflect.DeepEqual(root.Entries, want) {
				t.Fatal("exact unsorted membership", root.Entries, want)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			_, err = index.BuildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Catalog: view, Context: canceled}}, parent, def, store, nil)
			if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(root.Entries, want) {
				t.Fatal("canceled build changed predecessor", err)
			}
			idx := &catalog.Relation{SQLIndex: def}
			publication := &index.RowPublication{Writes: []storage.RowVersion{{Row: 1, Version: 4, Base: 1, Born: 2, Values: []sqltype.Value{sqltype.Null(typ)}}, {Row: 4, Version: 5, Born: 2, Values: []sqltype.Value{value}}}, SingleSlot: -1}
			interrupted := &exec.Context{EvalStatement: exec.EvalStatement{Catalog: view, Context: &spgistInterruptContext{Context: ctx, remaining: 2}}}
			if failed, err := index.MergeIndexRoot(interrupted, parent, idx, root.Entries, publication, true, true, false, "public", sqltype.OutputEnvironment{}, nil, nil); failed != nil || !errors.Is(err, context.Canceled) || !reflect.DeepEqual(root.Entries, want) {
				t.Fatal("interrupted merge changed predecessor", failed, err)
			}
			interrupted = &exec.Context{EvalStatement: exec.EvalStatement{Catalog: view, Context: &spgistInterruptContext{Context: ctx, remaining: 2}}}
			if failed, err := index.BuildIndexRoot(interrupted, parent, def, store, nil); failed != nil || !errors.Is(err, context.Canceled) || !reflect.DeepEqual(root.Entries, want) {
				t.Fatal("interrupted extraction changed predecessor", failed, err)
			}
			empty, err := index.BuildIndexRoot(eval, parent, def, &storage.Relation{}, nil)
			if err != nil || empty == nil || len(empty.Entries) != 0 {
				t.Fatal("empty membership", empty, err)
			}
			unchanged, err := index.MergeIndexRoot(eval, parent, idx, root.Entries, &index.RowPublication{SingleSlot: -1}, true, true, false, "public", sqltype.OutputEnvironment{}, nil, nil)
			if err != nil || unchanged != nil || !reflect.DeepEqual(root.Entries, want) {
				t.Fatal("no-op replaced predecessor", unchanged, err)
			}
			deleted, err := index.MergeIndexRoot(eval, parent, idx, root.Entries, &index.RowPublication{
				SingleSlot: -1,
				Deletion:   &visibility.PrivateDeletes{Rows: map[ids.RowID]visibility.DeleteCandidate{1: {Row: 1, Version: 1}}},
			}, true, true, false, "public", sqltype.OutputEnvironment{}, nil, nil)
			if err != nil || deleted == nil || !reflect.DeepEqual(deleted.Entries, want[1:]) || !reflect.DeepEqual(root.Entries, want) {
				t.Fatal("deletion-only membership changed predecessor", deleted, err)
			}
			nullStore := &storage.Relation{Versions: []storage.RowVersion{
				{Row: 8, Version: 8, Born: 1, Values: []sqltype.Value{sqltype.Null(typ)}},
				{Row: 9, Version: 9, Born: 1, Values: []sqltype.Value{sqltype.Null(typ)}},
			}}
			nulls, err := index.BuildIndexRoot(eval, parent, def, nullStore, nil)
			nullWant := []storage.IndexEntry{
				{Row: 8, Version: 8, Position: 0, Keys: []sqltype.Value{sqltype.Null(typ)}},
				{Row: 9, Version: 9, Position: 1, Keys: []sqltype.Value{sqltype.Null(typ)}},
			}
			if err != nil || nulls == nil || !reflect.DeepEqual(nulls.Entries, nullWant) {
				t.Fatal("all-NULL membership lost typed duplicates", nulls, err)
			}
			next, err := index.MergeIndexRoot(eval, parent, idx, root.Entries, publication, true, true, false, "public", sqltype.OutputEnvironment{}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(next.Entries) != 4 || next.Entries[0].Row != 2 || next.Entries[1].Row != 3 || next.Entries[2].Version != 4 || next.Entries[3].Version != 5 || !reflect.DeepEqual(root.Entries, want) {
				t.Fatal("immutable live delta", next.Entries)
			}
			column := plan.ColumnExpr{Info: expr.Info{Usage: typ.Usage(), Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1, Slot: 0}
			boolean, _ := view.LookupType(sqltype.Bool)
			predicate := plan.NullTestExpr{Info: expr.Info{Usage: boolean.Usage()}, Child: column, Not: true}
			partial := *def
			partial.Keys = []catalog.IndexKey{{Class: class, Derived: &catalog.DerivedIndexKey{Definition: column, Execution: column, Attributes: parent.Attributes}}}
			partial.Predicate = &catalog.IndexPredicate{Definition: predicate, Execution: predicate, Attributes: parent.Attributes}
			filtered, err := index.BuildIndexRoot(eval, parent, &partial, store, nil)
			if err != nil || len(filtered.Entries) != 2 || !reflect.DeepEqual(filtered.Entries[0], want[0]) || !reflect.DeepEqual(filtered.Entries[1], want[2]) {
				t.Fatal("partial derived membership lost exact values", filtered, err)
			}
		})
	}
}
