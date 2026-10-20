package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestInheritanceQueryClosure(t *testing.T) {
	e := New()
	p := inheritanceLifecycleTable(e, 20000, "p")
	l := inheritanceLifecycleTable(e, 20001, "l", p.Address())
	r := inheritanceLifecycleTable(e, 20002, "r", p.Address())
	d := inheritanceLifecycleTable(e, 20003, "d", l.Address(), r.Address(), p.Address())
	own := e.catalog.Domain.Session(1)
	peer := e.catalog.Domain.Session(2)
	ownNamespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20004}
	peerNamespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20005}
	o := inheritanceLifecycleTable(e, 20006, "own", p.Address())
	o.ObjectNamespace = ownNamespace
	f := inheritanceLifecycleTable(e, 20007, "foreign", p.Address())
	f.ObjectNamespace = peerNamespace
	branch := inheritanceLifecycleTable(e, 20008, "foreign_branch", f.Address())
	branch.ObjectNamespace = peerNamespace
	inheritanceLifecycleInstall(e, p, l, r, d, o, f, branch)
	e.catalog.state.SetDescriptor(ownNamespace, &catalog.Namespace{ObjectAddress: ownNamespace, Temporary: own})
	e.catalog.state.SetDescriptor(peerNamespace, &catalog.Namespace{ObjectAddress: peerNamespace, Temporary: peer})
	for range 2 {
		var got []ids.ObjectAddress
		if err := inheritanceQueryClosure(e.CatalogRead(), p, own, func(d *catalog.Relation) error { got = append(got, d.Address()); return nil }); err != nil {
			t.Fatal(err)
		}
		want := []ids.ObjectAddress{p.Address(), l.Address(), d.Address(), r.Address(), o.Address()}
		if !reflect.DeepEqual(got, want) {
			t.Fatal(got, want)
		}
	}
}

func inheritanceQueryUnitColumns(e *Engine, id ids.OID, names ...string) *catalog.Relation {
	d := inheritanceLifecycleTable(e, id, "layout")
	for i, name := range names {
		d.Columns = append(d.Columns, catalog.Column{Name: name, Usage: testUsage(e, sqltype.Int4)})
		d.Attributes = append(d.Attributes, int32(i+1))
		d.Index[name] = i
	}
	d.ColumnMeta.HighWater = int32(len(names))
	return d
}

func inheritanceQueryInt(e *Engine, n int64) sqltype.Value {
	value, err := sqltype.Integer(testUsage(e, sqltype.Int4).Type, n)
	if err != nil {
		panic(err)
	}
	return value
}

func TestInheritanceQueryLayout(t *testing.T) {
	e := New()
	parent := inheritanceQueryUnitColumns(e, 20000, "a", "x")
	child := inheritanceQueryUnitColumns(e, 20001, "y", "a", "x", "z")
	layout, err := inheritanceQueryLayout(parent, child)
	if err != nil || !reflect.DeepEqual(layout, []int{1, 2}) {
		t.Fatal(layout, err)
	}
	segment := inheritanceQuerySegment{layout: layout, scratch: make([]sqltype.Value, 2)}
	values := []sqltype.Value{inheritanceQueryInt(e, 9), inheritanceQueryInt(e, 2), inheritanceQueryInt(e, 3), inheritanceQueryInt(e, 8)}
	mapped := segment.mapValues(values)
	if !reflect.DeepEqual(mapped, values[1:3]) || &mapped[0] != &segment.scratch[0] {
		t.Fatal(mapped)
	}
	values[1] = inheritanceQueryInt(e, 4)
	if next := segment.mapValues(values); &next[0] != &mapped[0] || !reflect.DeepEqual(next, values[1:3]) {
		t.Fatal(next)
	}
	identity, err := inheritanceQueryLayout(parent, parent)
	if err != nil || identity != nil {
		t.Fatal(identity, err)
	}
	borrowed := (&inheritanceQuerySegment{}).mapValues(values)
	if &borrowed[0] != &values[0] {
		t.Fatal("identity mapping copied rows")
	}
	empty := inheritanceQueryUnitColumns(e, 20002)
	zero, err := inheritanceQueryLayout(empty, child)
	if err != nil || zero == nil || len(zero) != 0 {
		t.Fatal("zero-column projection lost nonidentity mapping", zero, err)
	}
	if values := (&inheritanceQuerySegment{layout: zero}).mapValues(values); len(values) != 0 {
		t.Fatal(values)
	}
	child.Columns[1].Usage = testUsage(e, sqltype.Int8)
	if _, err := inheritanceQueryLayout(parent, child); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal(err)
	}
}

func TestInheritanceQueryLocator(t *testing.T) {
	e := New()
	p := inheritanceQueryUnitColumns(e, 20000, "a")
	c := inheritanceQueryUnitColumns(e, 20001, "y", "a")
	ref := func(d *catalog.Relation) ids.ObjectRef {
		return e.catalog.Domain.Ref(e.CatalogRead().Epoch(), d.Address())
	}
	one := storage.RowVersion{Row: 1, Version: 1, Values: []sqltype.Value{inheritanceQueryInt(e, 1)}}
	two := storage.RowVersion{Row: 1, Version: 2, Values: []sqltype.Value{inheritanceQueryInt(e, 9), inheritanceQueryInt(e, 2)}}
	ps, cs := &storage.Relation{}, &storage.Relation{}
	parent := queryLeaf{ref: ref(p), relation: p, storage: ps}
	child := queryLeaf{ref: ref(c), relation: c, storage: cs}
	source := &querySource{ref: parent.ref, inheritance: []inheritanceQuerySegment{
		{leaf: parent, prefix: &querySource{committed: []storage.RowVersion{one}}},
		{leaf: child, prefix: &querySource{committed: []storage.RowVersion{{}, two}, visible: []int{1}}, layout: []int{1}, scratch: make([]sqltype.Value, 1)},
	}, inheritanceEnds: []int{1, 2}}
	first := source.physicalRow(0, parent, one)
	second := source.physicalRow(1, parent, two)
	if first.leaf.ref == second.leaf.ref || first.version.Row != second.version.Row || second.leaf.storage != cs || second.hint != 1 {
		t.Fatal(first, second)
	}
	if lockResourceForFirst, lockResourceForSecond := (lock.Resource{ObjectRef: first.leaf.ref, Row: first.version.Row}), (lock.Resource{ObjectRef: second.leaf.ref, Row: second.version.Row}); lockResourceForFirst == lockResourceForSecond {
		t.Fatal("equal RowID aliased physical resources")
	}
	latest := two
	latest.Version = 3
	latest.Values = []sqltype.Value{inheritanceQueryInt(e, 9), inheritanceQueryInt(e, 4)}
	if values := second.values(latest); len(values) != 1 || !reflect.DeepEqual(values, latest.Values[1:]) {
		t.Fatal(values)
	}
	if source.versionAt(1).Version != 2 || source.physicalRef(1) != child.ref || source.row(absentQueryIndex) != nil {
		t.Fatal("physical/absent provenance lost")
	}
}

func TestInheritanceQueryVisibility(t *testing.T) {
	e := New()
	p := inheritanceLifecycleTable(e, 20000, "p")
	c := inheritanceLifecycleTable(e, 20001, "c", p.Address())
	inheritanceLifecycleInstall(e, p, c)
	e.visibility.SetCommitSeqLocked(1)
	e.storage.Relations[p.Address()].Versions = []storage.RowVersion{{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{}}}
	e.storage.Relations[c.Address()].Versions = []storage.RowVersion{{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{}}, {Row: 2, Version: 2, Born: 1, Values: []sqltype.Value{}}}
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	if err = scope.gate(&parser.Select{}); err != nil {
		t.Fatal(err)
	}
	ref := e.catalog.Domain.Ref(e.CatalogRead().Epoch(), p.Address())
	leaf := queryLeaf{ref: ref, relation: p, storage: e.storage.Relations[p.Address()]}
	scope.querySources = []queryLeaf{leaf}
	if err = e.locks.Acquire(op.Context(), scope.ticket, lock.Resource{ObjectRef: ref}, lock.AccessShare); err != nil {
		t.Fatal(err)
	}
	if err = scope.acquireInheritanceSources(nil); err != nil {
		t.Fatal(err)
	}
	collectAuth, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	collectAuth.Recapture(scope.inheritance.catalog)
	e.mu.Lock()
	refs, err := e.queryTableRefsLocked(collectAuth, collectAuth.State.CurrentUser, scope.querySources, nil, op.session.token, queryRowMark{})
	var view *visibility.View
	if err == nil {
		view, err = scope.acquireDataViewsLocked(e, refs)
	}
	var source *querySource
	if err == nil {
		source, err = captureInheritanceQueryPrefix(scope.inheritance.catalog, view, leaf, op.session.token)
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	defer source.close()
	if err = selectQuerySources(op.Context(), source); err != nil || source.count() != 3 {
		t.Fatal(source.count(), err)
	}
	if source.view.Data() != view.Data() || source.inheritance[1].prefix.view.Data() != view.Data() || &source.inheritance[1].prefix.committed[0] != &e.storage.Relations[c.Address()].Versions[0] {
		t.Fatal("hierarchy acquired a second visibility owner or copied immutable rows")
	}
	e.mu.Lock()
	same, err := scope.acquireDataViewsLocked(e, refs)
	e.mu.Unlock()
	if err != nil || same != view {
		t.Fatal("statement did not reuse sole view", err)
	}
	retained := source.inheritance[1].prefix.view
	source.close()
	if retained.Load() != nil || view.Load() == nil {
		t.Fatal("capture release consumed statement authority")
	}
	// Exercise the real capture error path with an actually acquired incomplete
	// statement view, not a fabricated handle. Earlier retained segments close.
	failOp, failDone, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer failDone()
	failed, err := e.StartStatement(failOp)
	if err != nil {
		t.Fatal(err)
	}
	defer failed.Finish(nil)
	if err = failed.gate(&parser.Select{}); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	incomplete, err := failed.acquireDataViewsLocked(e, []ids.ObjectRef{ref})
	var partial *querySource
	if err == nil {
		partial, err = captureInheritanceQueryPrefix(incomplete.Catalog(), incomplete, leaf, failOp.session.token)
	}
	e.mu.Unlock()
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
		query := statement(t, tc.sql).(*parser.Select)
		tables := parser.FromTables(query.From)
		if len(tables) != 1 {
			t.Fatalf("relation syntax lost its source carrier: %T", query.From)
		}
		table := tables[0]
		if table.Only != tc.only || sourceOnly(&parser.Select{From: table}) != tc.only {
			t.Fatal(tc.sql, table.Only, tc.only)
		}
	}
	// The real replacement remapper preserves mode while reminting authority.
	e := New()
	p := inheritanceLifecycleTable(e, 20000, "p")
	c := inheritanceLifecycleTable(e, 20001, "c", p.Address())
	inheritanceLifecycleInstall(e, p, c)
	ref := e.catalog.Domain.Ref(e.CatalogRead().Epoch(), p.Address())
	for _, only := range []bool{false, true} {
		old := ref
		old.Epoch++
		frozen := &viewDefinition{sources: []viewLeaf{{ref: old, only: only}}, query: &BoundQuery{Root: &sourceNode{Ref: old}}}
		mapped, err := remapFrozenPresentation(e.CatalogRead(), frozen, nil, func(source ids.ObjectRef) ids.ObjectRef {
			if source == old {
				return ref
			}
			return source
		}, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }, nil)
		if err != nil {
			t.Fatal(err)
		}
		remapped := mapped.sources[0]
		if remapped.ref != ref || remapped.only != only || frozen.sources[0].ref != old || mapped.query.Root.(*sourceNode).Ref != ref {
			t.Fatal("replacement lost source mode or mutated frozen authority")
		}
		var got []ids.ObjectAddress
		err = inheritanceQueryLeaves(e.CatalogRead(), []queryLeaf{{ref: remapped.ref, only: remapped.only}}, ids.SessionToken{}, func(leaf queryLeaf) error { got = append(got, leaf.ref.Address); return nil })
		want := []ids.ObjectAddress{p.Address()}
		if !only {
			want = append(want, c.Address())
		}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal(got, want, err)
		}
	}
}
