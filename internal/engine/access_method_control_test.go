package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
)

func TestAccessMethodDropClosure(t *testing.T) {
	e := New()
	method, family, class, member := accessMethodClassUnit(t, e, "am_closure")
	base := e.CatalogRead()
	selected, ok := base.IndexClass(class.Address().ObjectID)
	if !ok {
		t.Fatal("missing class")
	}
	// The relation is an actual SQL-index descriptor, not an AM-only drop list.
	typ, _ := base.LookupType(sqltype.Int4)
	parent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: catalogUnitOID(t, e)}, ObjectNamespace: class.Namespace(), ObjectRevision: 1, ObjectName: "am_closure_rows", RelationKind: catalog.TableRelation, Owner: class.Owner, Columns: []catalog.Column{{Name: "n", Usage: typ.Usage()}}, Attributes: []int32{1}, Index: map[string]int{"n": 0}}
	idx := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: catalogUnitOID(t, e)}, ObjectNamespace: class.Namespace(), ObjectRevision: 1, ObjectName: "am_closure_i", RelationKind: catalog.IndexRelation, Owner: class.Owner, Columns: parent.Columns, Attributes: parent.Attributes, SQLIndex: &catalog.IndexDefinition{Method: catalog.BtreeMethod, MethodOID: method.Address().ObjectID, Parent: parent.Address(), Keys: []catalog.IndexKey{{Attribute: 1, Class: selected}}, Ready: true, Valid: true, Live: true}}
	state := catalog.CloneState(base.State())
	for _, d := range []catalog.Descriptor{parent, idx} {
		state.SetDescriptor(d.Address(), d)
		state.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	view := base.WithState(state)
	stage := e.BeginCatalogStage(view)
	defer e.DiscardCatalogStage(stage)
	edges, err := DomainDependencyClosure(context.Background(), stage, []ids.ObjectAddress{method.Address()})
	if err != nil {
		t.Fatal(err)
	}
	got := map[ids.ObjectAddress]table.DomainDependency{}
	for _, edge := range edges {
		if _, duplicate := got[edge.Address]; duplicate {
			t.Fatal("duplicate retirement", edge)
		}
		got[edge.Address] = edge
	}
	if len(got) != 4 {
		t.Fatal("incomplete method closure", got)
	}
	for _, address := range []ids.ObjectAddress{family.Address(), class.Address(), member.Address()} {
		if edge, ok := got[address]; !ok || !edge.Automatic || edge.Root != method.Address() {
			t.Fatal("AUTO/INTERNAL closure", address, edge)
		}
	}
	if edge := got[idx.Address()]; edge.Automatic || edge.Target != class.Address() || edge.Label != "index am_closure_i" {
		t.Fatal("normal index consumer", edge)
	}
	if err := DomainRestrictError(stage, []ids.ObjectAddress{method.Address()}, edges, 1, method.Address()); err == nil {
		t.Fatal("index RESTRICT accepted")
	}
	if e.CatalogRead().State() != base.State() || base.Get(method.Address()) != method {
		t.Fatal("RESTRICT discovery published")
	}
	deps := catalog.OperatorFamilyDependencies(base, family)
	if len(deps) != 2 || deps[1].Address != method.Address() || deps[1].Kind != 'a' {
		t.Fatal("family method edge", deps)
	}
	deps = catalog.OperatorFamilyDependencies(base, class)
	for _, edge := range deps {
		if edge.Address == method.Address() {
			t.Fatal("redundant class method edge")
		}
	}
	if !stored.DependPinned(base, nil, ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 330}) {
		t.Fatal("stock handler not pinned")
	}
}

func TestAccessMethodCaptureReplacement(t *testing.T) {
	e := New()
	method, family, class, member := accessMethodClassUnit(t, e, "am_capture")
	view := e.CatalogRead()
	source, ref := pgAMUnitSource(t, e)
	capture := func(ctx context.Context, v catalog.Read) [][]sqltype.Value {
		rows, err := captureCatalogSourceLocked(e, ctx, v, e.RoleStore.PublishedRead(&e.Catalog.Owner, v), ref, source)
		if err != nil {
			t.Fatal(err)
		}
		return rows.Anonymous
	}
	retained := capture(context.Background(), view)
	copied := capture(context.Background(), view)
	find := func(rows [][]sqltype.Value) []sqltype.Value {
		for _, row := range rows {
			oid, err := row[0].OID()
			if err != nil {
				t.Fatal(err)
			}
			if oid == uint32(method.Address().ObjectID) {
				return row
			}
		}
		t.Fatal("missing method capture")
		return nil
	}
	row := find(retained)
	row[1] = sqltype.String(source.Columns[1].Usage.Type, "mutated_private_capture")
	if name, _ := find(copied)[1].Text(); name != method.Name() {
		t.Fatal("captures share values")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := captureCatalogSourceLocked(e, ctx, view, e.RoleStore.PublishedRead(&e.Catalog.Owner, view), ref, source); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled capture", err)
	}
	selected, ok := view.IndexClass(class.Address().ObjectID)
	if !ok {
		t.Fatal("missing class")
	}
	selected, err := routine.BindClassSupport(view, selected)
	if err != nil {
		t.Fatal(err)
	}
	typ, _ := view.LookupType(sqltype.Int4)
	parent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: catalogUnitOID(t, e)}, ObjectNamespace: class.Namespace(), ObjectRevision: 1, ObjectName: "am_capture_rows", RelationKind: catalog.TableRelation, Owner: class.Owner, Columns: []catalog.Column{{Name: "n", Usage: typ.Usage()}}, Attributes: []int32{1}, Index: map[string]int{"n": 0}}
	idx := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: catalogUnitOID(t, e)}, ObjectNamespace: class.Namespace(), ObjectRevision: 1, ObjectName: "am_capture_i", RelationKind: catalog.IndexRelation, Owner: class.Owner, Columns: parent.Columns, Attributes: parent.Attributes, SQLIndex: &catalog.IndexDefinition{Method: catalog.BtreeMethod, MethodOID: method.Address().ObjectID, Parent: parent.Address(), Keys: []catalog.IndexKey{{Attribute: 1, Class: selected}}, Ready: true, Valid: true, Live: true}}
	next := catalog.CloneState(view.State())
	for _, d := range []catalog.Descriptor{parent, idx} {
		next.SetDescriptor(d.Address(), d)
		next.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	destination := ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: catalogUnitOID(t, e)}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{method.Address(): destination}
	if err := control.RemapReplacementIdentities(view, next, nil, mapping, map[ids.ObjectAddress]*storage.Relation{}, map[ids.ObjectAddress]*storage.SequenceGenerator{}, nil); err != nil {
		t.Fatal(err)
	}
	remapped := view.WithState(next)
	m, ok := remapped.AccessMethod(destination.ObjectID)
	if !ok || m == method || m.Native != catalog.BtreeAccessMethod || m.Handler != 330 {
		t.Fatal("method remap", m)
	}
	if _, ok := remapped.AccessMethod(method.Address().ObjectID); ok {
		t.Fatal("source AM address retained")
	}
	f, _ := remapped.OperatorFamily(family.Address())
	c, _ := remapped.OperatorClass(class.Address().ObjectID)
	if f.Method != destination.ObjectID || c.Method != destination.ObjectID {
		t.Fatal("family/class method not remapped", f, c)
	}
	r, _ := remapped.Relation(idx.Address())
	if r.SQLIndex.MethodIdentity() != destination.ObjectID || r.SQLIndex.Keys[0].Class.MethodOID() != uint32(destination.ObjectID) || r.SQLIndex.Keys[0].Class.Support(1).Routine != 0 {
		t.Fatal("source operation capture transplanted", r.SQLIndex)
	}
	rebound, err := routine.BindClassSupport(remapped, r.SQLIndex.Keys[0].Class)
	if err != nil || rebound.Support(1).Routine != 351 || rebound.Support(1).Member != uint32(member.Address().ObjectID) {
		t.Fatal("destination support not rebuilt", rebound, err)
	}
	if method.Address() == destination || selected.MethodOID() != uint32(method.Address().ObjectID) || selected.Support(1).Routine != 351 {
		t.Fatal("source definition changed")
	}
	for _, name := range []string{"can_order", "can_unique", "can_multi_col", "can_exclude", "can_include"} {
		physical, ok := m.IndexImplementation()
		if !ok {
			t.Fatal("lost physical method")
		}
		if value, known := index.MethodProperty(physical, name); !known || !value {
			t.Fatal("remapped inquiry", name)
		}
	}
	// Materialized pg_depend suppresses the stock pinned handler, while retaining
	// the actual automatic family link to the remapped method.
	edges, err := pgcatalog.CollectDepend(context.Background(), remapped, EngineCatalogRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	familyEdge := false
	for _, edge := range edges {
		if edge.Dependent == destination && edge.Referenced.ClassID == ids.ProcedureClassID {
			t.Fatal("pinned handler dependency materialized", edge)
		}
		if edge.Dependent == family.Address() && edge.Referenced == destination && edge.DepType == 'a' {
			familyEdge = true
		}
	}
	if !familyEdge {
		t.Fatal("remapped method family dependency missing")
	}
	if name, _ := find(copied)[1].Text(); name != method.Name() {
		t.Fatal("replacement changed retained capture")
	}
	// A genuinely foreign engine must select its own installed handler and
	// support/member descriptors before rebuilding any live root.
	foreign := New()
	accessMethodClassUnit(t, foreign, "am_destination_occupied")
	dm, df, dc, ds := accessMethodClassUnit(t, foreign, "am_capture")
	destinationView := foreign.CatalogRead()
	sp, _ := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 330})
	dp, _ := destinationView.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 330})
	if sp == dp || dp.AccessMethod != catalog.BtreeAccessMethod {
		t.Fatal("source native handler pointer transplanted")
	}
	destinationClass, ok := destinationView.IndexClass(dc.Address().ObjectID)
	if !ok {
		t.Fatal("missing destination class")
	}
	destinationClass, err = routine.BindClassSupport(destinationView, destinationClass)
	if err != nil || destinationClass.Support(1).Member != uint32(ds.Address().ObjectID) || destinationClass.Support(1).Family != uint32(df.Address().ObjectID) || destinationClass.MethodOID() != uint32(dm.Address().ObjectID) {
		t.Fatal("foreign binding reused source authority", destinationClass, err)
	}
	dt, _ := destinationView.LookupType(sqltype.Int4)
	dv := func(n int64) sqltype.Value {
		value, err := sqltype.Integer(dt, n)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	destinationParent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: catalogUnitOID(t, foreign)}, ObjectRevision: 1, RelationKind: catalog.TableRelation, Attributes: []int32{1}, Columns: []catalog.Column{{Name: "n", Usage: dt.Usage()}}, Index: map[string]int{"n": 0}}
	destinationDefinition := &catalog.IndexDefinition{Method: catalog.BtreeMethod, MethodOID: dm.Address().ObjectID, Parent: destinationParent.Address(), Keys: []catalog.IndexKey{{Attribute: 1, Class: destinationClass}}, Ready: true, Valid: true, Live: true}
	destinationIndex := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: catalogUnitOID(t, foreign)}, SQLIndex: destinationDefinition}
	store := &storage.Relation{}
	for i, v := range []sqltype.Value{dv(2), dv(1), dv(2), sqltype.Null(dt)} {
		store.Versions = append(store.Versions, storage.RowVersion{Row: ids.RowID(i + 1), Version: ids.VersionID(i + 1), Born: 1, Values: []sqltype.Value{v}})
	}
	evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: destinationView}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	root, err := index.BuildIndexRoot(evaluation, destinationParent, destinationDefinition, store, nil)
	if err != nil || len(root.Entries) != 4 || root.Entries[0].Row != 2 || root.Entries[3].Row != 4 {
		t.Fatal("foreign rebuilt root", root, err)
	}
	inserted := storage.RowVersion{Row: 5, Version: 5, Values: []sqltype.Value{dv(0)}}
	delta := &index.RowPublication{Store: store, Root: store.Versions, Next: append(append([]storage.RowVersion(nil), store.Versions...), inserted), Writes: []storage.RowVersion{inserted}, SingleSlot: -1}
	maintained, err := index.MergeIndexRoot(evaluation, destinationParent, destinationIndex, root.Entries, delta, false, true, false, "", sqltype.OutputEnvironment{}, nil, nil)
	if err != nil || len(maintained.Entries) != 5 || maintained.Entries[0].Row != 5 || maintained.Entries[0].Version != 5 || len(root.Entries) != 4 || root.Entries[0].Row != 2 {
		t.Fatal("foreign maintained/retained root", maintained, err)
	}
}
