package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestInheritanceQueryClosure(t *testing.T) {
	e := engine.New()
	p := inheritanceLifecycleTable(e, 20000, "p")
	l := inheritanceLifecycleTable(e, 20001, "l", p.Address())
	r := inheritanceLifecycleTable(e, 20002, "r", p.Address())
	d := inheritanceLifecycleTable(e, 20003, "d", l.Address(), r.Address(), p.Address())
	own := e.Catalog.Domain.Session(1)
	peer := e.Catalog.Domain.Session(2)
	ownNamespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20004}
	peerNamespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20005}
	o := inheritanceLifecycleTable(e, 20006, "own", p.Address())
	o.ObjectNamespace = ownNamespace
	f := inheritanceLifecycleTable(e, 20007, "foreign", p.Address())
	f.ObjectNamespace = peerNamespace
	branch := inheritanceLifecycleTable(e, 20008, "foreign_branch", f.Address())
	branch.ObjectNamespace = peerNamespace
	inheritanceLifecycleInstall(e, p, l, r, d, o, f, branch)
	e.Catalog.State.SetDescriptor(ownNamespace, &catalog.Namespace{ObjectAddress: ownNamespace, Temporary: own})
	e.Catalog.State.SetDescriptor(peerNamespace, &catalog.Namespace{ObjectAddress: peerNamespace, Temporary: peer})
	for range 2 {
		var got []ids.ObjectAddress
		if err := table.InheritanceQueryClosure(e.CatalogRead(), p, own, func(d *catalog.Relation) error { got = append(got, d.Address()); return nil }); err != nil {
			t.Fatal(err)
		}
		want := []ids.ObjectAddress{p.Address(), l.Address(), d.Address(), r.Address(), o.Address()}
		if !reflect.DeepEqual(got, want) {
			t.Fatal(got, want)
		}
	}
}

func inheritanceQueryUnitColumns(e *core.Engine, id ids.OID, names ...string) *catalog.Relation {
	d := inheritanceLifecycleTable(e, id, "layout")
	for i, name := range names {
		d.Columns = append(d.Columns, catalog.Column{Name: name, Usage: enginetest.Usage(e, sqltype.Int4)})
		d.Attributes = append(d.Attributes, int32(i+1))
		d.Index[name] = i
	}
	d.ColumnMeta.HighWater = int32(len(names))
	return d
}

func inheritanceQueryInt(e *core.Engine, n int64) sqltype.Value {
	value, err := sqltype.Integer(enginetest.Usage(e, sqltype.Int4).Type, n)
	if err != nil {
		panic(err)
	}
	return value
}

func TestInheritanceQueryLayout(t *testing.T) {
	e := engine.New()
	parent := inheritanceQueryUnitColumns(e, 20000, "a", "x")
	child := inheritanceQueryUnitColumns(e, 20001, "y", "a", "x", "z")
	layout, err := catalog.InheritanceQueryLayout(parent, child)
	if err != nil || !reflect.DeepEqual(layout, []int{1, 2}) {
		t.Fatal(layout, err)
	}
	segment := exec.InheritanceQuerySegment{Layout: layout, Scratch: make([]sqltype.Value, 2)}
	values := []sqltype.Value{inheritanceQueryInt(e, 9), inheritanceQueryInt(e, 2), inheritanceQueryInt(e, 3), inheritanceQueryInt(e, 8)}
	mapped := segment.MapValues(values)
	if !reflect.DeepEqual(mapped, values[1:3]) || &mapped[0] != &segment.Scratch[0] {
		t.Fatal(mapped)
	}
	values[1] = inheritanceQueryInt(e, 4)
	if next := segment.MapValues(values); &next[0] != &mapped[0] || !reflect.DeepEqual(next, values[1:3]) {
		t.Fatal(next)
	}
	identity, err := catalog.InheritanceQueryLayout(parent, parent)
	if err != nil || identity != nil {
		t.Fatal(identity, err)
	}
	borrowed := (&exec.InheritanceQuerySegment{}).MapValues(values)
	if &borrowed[0] != &values[0] {
		t.Fatal("identity mapping copied rows")
	}
	empty := inheritanceQueryUnitColumns(e, 20002)
	zero, err := catalog.InheritanceQueryLayout(empty, child)
	if err != nil || zero == nil || len(zero) != 0 {
		t.Fatal("zero-column projection lost nonidentity mapping", zero, err)
	}
	if values := (&exec.InheritanceQuerySegment{Layout: zero}).MapValues(values); len(values) != 0 {
		t.Fatal(values)
	}
	child.Columns[1].Usage = enginetest.Usage(e, sqltype.Int8)
	if _, err := catalog.InheritanceQueryLayout(parent, child); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal(err)
	}
}

func TestInheritanceQueryLocator(t *testing.T) {
	e := engine.New()
	p := inheritanceQueryUnitColumns(e, 20000, "a")
	c := inheritanceQueryUnitColumns(e, 20001, "y", "a")
	ref := func(d *catalog.Relation) ids.ObjectRef {
		return e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), d.Address())
	}
	one := storage.RowVersion{Row: 1, Version: 1, Values: []sqltype.Value{inheritanceQueryInt(e, 1)}}
	two := storage.RowVersion{Row: 1, Version: 2, Values: []sqltype.Value{inheritanceQueryInt(e, 9), inheritanceQueryInt(e, 2)}}
	ps, cs := &storage.Relation{}, &storage.Relation{}
	parent := exec.Leaf{Ref: ref(p), Relation: p, Storage: ps}
	child := exec.Leaf{Ref: ref(c), Relation: c, Storage: cs}
	source := &exec.Source{Ref: parent.Ref, Inheritance: []exec.InheritanceQuerySegment{
		{Leaf: parent, Prefix: &exec.Source{Committed: []storage.RowVersion{one}}},
		{Leaf: child, Prefix: &exec.Source{Committed: []storage.RowVersion{{}, two}, Visible: []int{1}}, Layout: []int{1}, Scratch: make([]sqltype.Value, 1)},
	}, InheritanceEnds: []int{1, 2}}
	first := source.PhysicalRow(0, parent, one)
	second := source.PhysicalRow(1, parent, two)
	if first.Leaf.Ref == second.Leaf.Ref || first.Version.Row != second.Version.Row || second.Leaf.Storage != cs || second.Hint != 1 {
		t.Fatal(first, second)
	}
	if lockResourceForFirst, lockResourceForSecond := (lock.Resource{ObjectRef: first.Leaf.Ref, Row: first.Version.Row}), (lock.Resource{ObjectRef: second.Leaf.Ref, Row: second.Version.Row}); lockResourceForFirst == lockResourceForSecond {
		t.Fatal("equal RowID aliased physical resources")
	}
	latest := two
	latest.Version = 3
	latest.Values = []sqltype.Value{inheritanceQueryInt(e, 9), inheritanceQueryInt(e, 4)}
	if values := second.Values(latest); len(values) != 1 || !reflect.DeepEqual(values, latest.Values[1:]) {
		t.Fatal(values)
	}
	if source.VersionAt(1).Version != 2 || source.PhysicalRef(1) != child.Ref || source.Row(exec.AbsentQueryIndex) != nil {
		t.Fatal("physical/absent provenance lost")
	}
}

func TestInheritanceQueryVisibility(t *testing.T) {
	e := engine.New()
	p := inheritanceLifecycleTable(e, 20000, "p")
	c := inheritanceLifecycleTable(e, 20001, "c", p.Address())
	inheritanceLifecycleInstall(e, p, c)
	e.Visibility.SetCommitSeqLocked(1)
	e.Storage.Relations[p.Address()].Versions = []storage.RowVersion{{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{}}}
	e.Storage.Relations[c.Address()].Versions = []storage.RowVersion{{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{}}, {Row: 2, Version: 2, Born: 1, Values: []sqltype.Value{}}}
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	if err = scope.Gate(&parser.Select{}); err != nil {
		t.Fatal(err)
	}
	ref := e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), p.Address())
	leaf := exec.Leaf{Ref: ref, Relation: p, Storage: e.Storage.Relations[p.Address()]}
	scope.QuerySources = []exec.Leaf{leaf}
	if err = e.Locks.Acquire(op.Context(), scope.LockTicket, lock.Resource{ObjectRef: ref}, lock.AccessShare); err != nil {
		t.Fatal(err)
	}
	if err = core.ScopeAcquireInheritanceSources(scope, nil); err != nil {
		t.Fatal(err)
	}
	collectAuth, err := core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	collectAuth.Recapture(core.InheritanceScopeCatalog(&scope.Inheritance))
	e.Mu.Lock()
	refs, err := e.QueryTableRefsLocked(collectAuth, collectAuth.State.CurrentUser, scope.QuerySources, nil, op.Session.SessionToken, plan.RowMark{})
	var view *visibility.View
	if err == nil {
		view, err = scope.AcquireDataViewsLocked(e, refs)
	}
	var source *exec.Source
	if err == nil {
		source, err = table.CaptureInheritanceQueryPrefix(core.InheritanceScopeCatalog(&scope.Inheritance), view, leaf, op.Session.SessionToken)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if err = exec.SelectQuerySources(op.Context(), source); err != nil || source.Count() != 3 {
		t.Fatal(source.Count(), err)
	}
	if source.View.Data() != view.Data() || source.Inheritance[1].Prefix.View.Data() != view.Data() || &source.Inheritance[1].Prefix.Committed[0] != &e.Storage.Relations[c.Address()].Versions[0] {
		t.Fatal("hierarchy acquired a second visibility owner or copied immutable rows")
	}
	e.Mu.Lock()
	same, err := scope.AcquireDataViewsLocked(e, refs)
	e.Mu.Unlock()
	if err != nil || same != view {
		t.Fatal("statement did not reuse sole view", err)
	}
	retained := source.Inheritance[1].Prefix.View
	source.Close()
	if retained.Load() != nil || view.Load() == nil {
		t.Fatal("capture release consumed statement authority")
	}
	// Exercise the real capture error path with an actually acquired incomplete
	// statement view, not a fabricated handle. Earlier retained segments close.
	failOp, failDone, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer failDone()
	failed, err := e.StartStatement(failOp)
	if err != nil {
		t.Fatal(err)
	}
	defer failed.Finish(nil)
	if err = failed.Gate(&parser.Select{}); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	incomplete, err := failed.AcquireDataViewsLocked(e, []ids.ObjectRef{ref})
	var partial *exec.Source
	if err == nil {
		partial, err = table.CaptureInheritanceQueryPrefix(incomplete.Catalog(), incomplete, leaf, failOp.Session.SessionToken)
	}
	e.Mu.Unlock()
	if err == nil || partial != nil || incomplete == nil || incomplete.Load() == nil {
		t.Fatal("partial capture leaked a result or consumed sole view", partial, err)
	}
	failed.Finish(err)
	if incomplete.Load() != nil {
		t.Fatal("failed statement retained snapshot authority")
	}
}

func TestInheritanceQueryScanMode(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		only bool
	}{
		{"SELECT a FROM p", false}, {"SELECT a FROM p*", false},
		{"SELECT a FROM ONLY p", true}, {"SELECT a FROM ONLY(p)", true},
		{`SELECT a FROM ONLY "public"."p" AS named(a)`, true},
	} {
		query := enginetest.Statement(t, tc.sql).(*parser.Select)
		tables := parser.FromTables(query.From)
		if len(tables) != 1 {
			t.Fatalf("relation syntax lost its source carrier: %T", query.From)
		}
		tbl := tables[0]
		if tbl.Only != tc.only || table.SourceOnly(&parser.Select{From: tbl}) != tc.only {
			t.Fatal(tc.sql, tbl.Only, tc.only)
		}
	}
	// The real replacement remapper preserves mode while reminting authority.
	e := engine.New()
	p := inheritanceLifecycleTable(e, 20000, "p")
	c := inheritanceLifecycleTable(e, 20001, "c", p.Address())
	inheritanceLifecycleInstall(e, p, c)
	ref := e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), p.Address())
	for _, only := range []bool{false, true} {
		old := ref
		old.Epoch++
		frozen := stored.NewView(stored.ViewParts{Sources: []stored.ViewLeaf{{Ref: old, Only: only}}, Query: &plan.Query{Root: &plan.SourceNode{Ref: old}}})
		mapped, err := stored.RemapView(e.CatalogRead(), frozen, nil, func(source ids.ObjectRef) ids.ObjectRef {
			if source == old {
				return ref
			}
			return source
		}, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		remapped := mapped.Sources()[0]
		if remapped.Ref != ref || remapped.Only != only || frozen.Sources()[0].Ref != old || mapped.Query().Root.(*plan.SourceNode).Ref != ref {
			t.Fatal("replacement lost source mode or mutated frozen authority")
		}
		var got []ids.ObjectAddress
		err = table.InheritanceQueryLeaves(e.CatalogRead(), []exec.Leaf{{Ref: remapped.Ref, Only: remapped.Only}}, ids.SessionToken{}, func(leaf exec.Leaf) error { got = append(got, leaf.Ref.Address); return nil })
		want := []ids.ObjectAddress{p.Address()}
		if !only {
			want = append(want, c.Address())
		}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal(got, want, err)
		}
	}
}
