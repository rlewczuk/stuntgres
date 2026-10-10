package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

type partialIndexFixture struct {
	e             *Engine
	parent, index *relationDescriptor
	predicate     columnExpression
	store         *relationStorage
}

func newPartialIndexFixture(t *testing.T) partialIndexFixture {
	t.Helper()
	e := New()
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
	parent := &relationDescriptor{address: ObjectAddress{ClassID: RelationClassID, ObjectID: 20000}, namespace: ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}, name: "t", revision: 1, relationKind: tableRelation, attributes: []int32{10, 20}, attributeHighWater: 20, columns: []ColumnDescriptor{{Name: "k", Usage: integer.Usage()}, {Name: "active", Usage: boolean.Usage()}}, index: map[string]int{"k": 0, "active": 1}}
	ref := ObjectRef{owner: e.owner, Epoch: e.state.epoch, Address: parent.address}
	ref.Address.SubID = 20
	column := columnExpression{expressionInfo: expressionInfo{usage: boolean.Usage(), properties: ExprProperties{RowDependent: true}}, scope: 1, frame: 1, slot: 1, origin: ref}
	predicate := &indexPredicate{definition: column, execution: column, attributes: parent.attributes, dependencies: []ObjectRef{ref}}
	class, _ := sqltype.DefaultIndexClass(integer)
	index := &relationDescriptor{address: ObjectAddress{ClassID: RelationClassID, ObjectID: 20001}, namespace: parent.namespace, name: "pi", revision: 1, relationKind: indexRelation, columns: []ColumnDescriptor{parent.columns[0]}, attributes: []int32{1}, attributeHighWater: 1, index: map[string]int{"k": 0}, sqlIndex: &indexDefinition{parent: parent.address, method: btreeMethod, ready: true, valid: true, live: true, keys: []indexKey{{attribute: 10, class: class}}, predicate: predicate}}
	store := &relationStorage{}
	for i, k := range []int64{10, 20, 30, 0, 10} {
		value, _ := sqltype.Integer(integer, k)
		if i == 3 {
			value = sqltype.Null(integer)
		}
		active := sqltype.Boolean(boolean, i == 0 || i == 3 || i == 4)
		if i == 2 {
			active = sqltype.Null(boolean)
		}
		store.versions = append(store.versions, rowVersion{row: RowID(i + 1), version: VersionID(i + 1), values: []sqltype.Value{value, active}})
	}
	e.state.descriptors[parent.address], e.state.descriptors[index.address] = parent, index
	e.storage[parent.address] = store
	root, err := buildIndexRoot(context.Background(), parent, index.sqlIndex, store)
	if err != nil {
		t.Fatal(err)
	}
	e.indexRoots = map[ObjectAddress]*logicalIndexRoot{index.address: root}
	return partialIndexFixture{e, parent, index, column, store}
}

func partialWantMembership(t *testing.T, root *logicalIndexRoot, want map[RowID]VersionID) {
	t.Helper()
	if !reflect.DeepEqual(root.membership, want) || len(root.entries) != len(want) {
		t.Fatal("wrong membership", root.membership, want, root.entries)
	}
	for _, entry := range root.entries {
		if root.membership[entry.row] != entry.version {
			t.Fatal("entry/lookup disagreement", entry)
		}
	}
}

func TestPartialIndexPredicateTruth(t *testing.T) {
	f := newPartialIndexFixture(t)
	evaluation := EvalContext{Context: context.Background(), Scope: 1, Frame: 1}
	for i, row := range f.store.versions {
		got, err := indexPredicateMatches(&evaluation, f.index.sqlIndex.predicate, row.values)
		if err != nil || got != (i == 0 || i == 3 || i == 4) {
			t.Fatal(i, got, err)
		}
		got, err = indexPredicateMatches(&evaluation, nil, row.values)
		if err != nil || !got {
			t.Fatal("absent predicate", got, err)
		}
	}
	boolean := f.predicate.usage.Type
	for _, value := range []sqltype.Value{sqltype.Boolean(boolean, true), sqltype.Boolean(boolean, false), sqltype.Null(boolean)} {
		node := literalExpression{expressionInfo: expressionInfo{usage: boolean.Usage()}, value: value}
		p := &indexPredicate{definition: node, execution: node}
		got, err := indexPredicateMatches(&evaluation, p, nil)
		truth, _ := TruthOf(value)
		if err != nil || got != (truth == True) {
			t.Fatal(value, got, err)
		}
	}
}

func TestPartialIndexRootMembership(t *testing.T) {
	f := newPartialIndexFixture(t)
	root := f.e.indexRoots[f.index.address]
	partialWantMembership(t, root, map[RowID]VersionID{1: 1, 4: 4, 5: 5})
	if got := btreeKeys(t, root); !reflect.DeepEqual(got, []string{"10", "10", "NULL"}) {
		t.Fatal(got)
	}
	if len(f.store.versions) != 5 {
		t.Fatal("build changed table")
	}
	ordinary := *f.index.sqlIndex
	ordinary.predicate = nil
	all, err := buildIndexRoot(context.Background(), f.parent, &ordinary, f.store)
	if err != nil || len(all.entries) != 5 || all.membership != nil {
		t.Fatal("ordinary path", all, err)
	}
	for _, tc := range []struct {
		value sqltype.Value
		count int
	}{{sqltype.Boolean(f.predicate.usage.Type, true), 5}, {sqltype.Boolean(f.predicate.usage.Type, false), 0}, {sqltype.Null(f.predicate.usage.Type), 0}} {
		node := literalExpression{expressionInfo: expressionInfo{usage: f.predicate.usage}, value: tc.value}
		def := *f.index.sqlIndex
		def.predicate = &indexPredicate{definition: node, execution: node}
		root, err := buildIndexRoot(context.Background(), f.parent, &def, f.store)
		if err != nil || len(root.entries) != tc.count || len(root.membership) != tc.count {
			t.Fatal("constant root", tc, root, err)
		}
	}
}

func TestPartialIndexMembershipTransitions(t *testing.T) {
	f := newPartialIndexFixture(t)
	old := f.e.indexRoots[f.index.address]
	boolean := f.predicate.usage.Type
	write := func(id RowID, version, base VersionID, active sqltype.Value) rowVersion {
		row := f.store.versions[id-1]
		row.version, row.base = version, base
		row.values = append([]sqltype.Value(nil), row.values...)
		row.values[1] = active
		return row
	}
	p := &rowPublication{writes: []rowVersion{
		write(1, 6, 1, sqltype.Boolean(boolean, false)),
		write(2, 7, 2, sqltype.Boolean(boolean, true)),
		write(3, 8, 3, sqltype.Boolean(boolean, true)),
		write(4, 9, 4, sqltype.Null(boolean)),
		write(5, 10, 5, sqltype.Boolean(boolean, true)),
	}}
	next, err := mergeIndexRoot(context.Background(), f.parent, f.index, old.entries, p, false, true, "", sqltype.OutputEnvironment{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	partialWantMembership(t, next, map[RowID]VersionID{2: 7, 3: 8, 5: 10})
	partialWantMembership(t, old, map[RowID]VersionID{1: 1, 4: 4, 5: 5})
	p = &rowPublication{deletion: &privateDeletionRoot{rows: map[RowID]deleteCandidate{2: {version: 7}, 1: {version: 6}}}}
	next, err = mergeIndexRoot(context.Background(), f.parent, f.index, next.entries, p, false, true, "", sqltype.OutputEnvironment{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	partialWantMembership(t, next, map[RowID]VersionID{3: 8, 5: 10})
	// Excluded successors and own-deleted additions must never resurrect identities.
	p = &rowPublication{writes: []rowVersion{write(3, 11, 8, sqltype.Boolean(boolean, false)), write(2, 12, 0, sqltype.Boolean(boolean, true))}, deletion: &privateDeletionRoot{rows: map[RowID]deleteCandidate{2: {version: 12, own: true}}}}
	next, err = mergeIndexRoot(context.Background(), f.parent, f.index, next.entries, p, false, true, "", sqltype.OutputEnvironment{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	partialWantMembership(t, next, map[RowID]VersionID{5: 10})
}

// Test-only execution witness: excluded rows must not call the key provider.
type partialEvaluationWitness struct {
	expressionInfo
	calls   *int
	failure error
	value   sqltype.Value
}

func (w partialEvaluationWitness) Eval(c *EvalContext) (sqltype.Value, error) {
	*w.calls++
	return w.value, w.failure
}

func TestPartialIndexPrivatePreflight(t *testing.T) {
	f := newPartialIndexFixture(t)
	op, done, err := testOperation(f.e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	scope := &StatementScope{op: op, txRef: TxRef{epoch: f.e.state.epoch}}
	calls := 0
	failure := errors.New("qualifying key")
	key := partialEvaluationWitness{expressionInfo: expressionInfo{usage: f.parent.columns[0].Usage}, calls: &calls, failure: failure}
	def := *f.index.sqlIndex
	def.keys = []indexKey{{class: f.index.sqlIndex.keys[0].class, derived: &derivedIndexKey{definition: key, execution: key}}}
	idx := *f.index
	idx.sqlIndex = &def
	f.e.state.descriptors[idx.address] = &idx
	scope.stage.rows = map[ObjectAddress][]rowVersion{f.parent.address: {f.store.versions[1], f.store.versions[2]}}
	if err := scope.validatePrivateIndexExpressions(nil, nil); err != nil || calls != 0 {
		t.Fatal("excluded keys evaluated", calls, err)
	}
	excluded := &relationStorage{versions: scope.stage.rows[f.parent.address]}
	built, err := buildIndexRoot(context.Background(), f.parent, &def, excluded)
	if err != nil || calls != 0 || len(built.entries) != 0 {
		t.Fatal("build evaluated excluded keys", calls, err)
	}
	merged, err := mergeIndexRoot(context.Background(), f.parent, &idx, built.entries, &rowPublication{writes: excluded.versions}, false, true, "", sqltype.OutputEnvironment{}, nil)
	if err != nil || calls != 0 || merged != nil {
		t.Fatal("excluded writes changed root or evaluated keys", merged, calls, err)
	}
	scope.stage.rows[f.parent.address] = []rowVersion{f.store.versions[0]}
	if err := scope.validatePrivateIndexExpressions(nil, nil); !errors.Is(err, failure) || calls != 1 {
		t.Fatal("qualifying key not vetoed", calls, err)
	}
	// Simple keys still preflight predicate failures, rather than waiting for COMMIT.
	calls = 0
	predicate := partialEvaluationWitness{expressionInfo: f.predicate.expressionInfo, calls: &calls, failure: failure}
	def.keys = f.index.sqlIndex.keys
	def.predicate = &indexPredicate{definition: predicate, execution: predicate}
	if err := scope.validatePrivateIndexExpressions(nil, nil); !errors.Is(err, failure) || calls != 1 {
		t.Fatal("simple-key predicate not preflighted", calls, err)
	}
	calls = 0
	if _, err := buildIndexRoot(context.Background(), f.parent, &def, f.store); !errors.Is(err, failure) || calls != 1 {
		t.Fatal("build predicate error", calls, err)
	}
	calls = 0
	if _, err := mergeIndexRoot(context.Background(), f.parent, &idx, f.e.indexRoots[f.index.address].entries, &rowPublication{writes: scope.stage.rows[f.parent.address]}, false, true, "", sqltype.OutputEnvironment{}, nil); !errors.Is(err, failure) || calls != 1 {
		t.Fatal("merge predicate error", calls, err)
	}
}

func TestPartialIndexPredicateCancellation(t *testing.T) {
	f := newPartialIndexFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	evaluation := EvalContext{Context: ctx, Scope: 1, Frame: 1}
	for _, p := range []*indexPredicate{nil, f.index.sqlIndex.predicate} {
		if _, err := indexPredicateMatches(&evaluation, p, nil); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	root := f.e.indexRoots[f.index.address]
	if _, err := buildIndexRoot(ctx, f.parent, f.index.sqlIndex, f.store); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := mergeIndexRoot(ctx, f.parent, f.index, root.entries, &rowPublication{}, false, true, "", sqltype.OutputEnvironment{}, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	partialWantMembership(t, root, map[RowID]VersionID{1: 1, 4: 4, 5: 5})
}

func TestPartialIndexImplication(t *testing.T) {
	f := newPartialIndexFixture(t)
	active := BoundExpr(f.predicate)
	boolean := f.predicate.usage
	and := func(a, b BoundExpr) BoundExpr {
		return logicalExpression{expressionInfo: expressionInfo{usage: boolean}, op: parser.LogicalAnd, left: a, right: b}
	}
	or := func(a, b BoundExpr) BoundExpr {
		return logicalExpression{expressionInfo: expressionInfo{usage: boolean}, op: parser.LogicalOr, left: a, right: b}
	}
	param := parameterExpression{expressionInfo: expressionInfo{usage: boolean, properties: ExprProperties{ParameterDependent: true}}, number: 1}
	fixed := literalExpression{expressionInfo: expressionInfo{usage: boolean}, value: sqltype.Boolean(boolean.Type, true)}
	other := f.predicate
	other.origin.Address.SubID++
	for _, tc := range []struct {
		q, p BoundExpr
		want bool
	}{
		{active, active, true}, {and(active, param), active, true}, {param, active, false}, {or(active, param), active, false},
		{and(fixed, and(active, param)), and(active, fixed), true}, {active, and(active, fixed), false},
		{other, active, false}, {active, nil, true}, {nil, active, false}, {param, param, false},
		{or(active, fixed), or(active, fixed), true},
	} {
		if got := partialPredicateImpliedBy(tc.q, tc.p); got != tc.want {
			t.Fatal(tc, got)
		}
	}
	other = f.predicate
	other.pos = parser.Position{Character: 999}
	if !partialPredicateImpliedBy(other, active) {
		t.Fatal("position changed identity")
	}
	other = f.predicate
	other.usage = f.parent.columns[0].Usage
	if partialPredicateImpliedBy(other, active) {
		t.Fatal("type mismatch proved")
	}
	unknown := partialEvaluationWitness{expressionInfo: f.predicate.expressionInfo}
	if partialPredicateImpliedBy(unknown, unknown) {
		t.Fatal("unknown node proved")
	}
	other = f.predicate
	other.collationExplicit = parser.Position{Character: 1}
	if partialPredicateImpliedBy(other, active) {
		t.Fatal("explicit collation provenance mismatch proved")
	}
	explicit := other
	other.collationExplicit.Character = 2
	if !partialPredicateImpliedBy(other, explicit) {
		t.Fatal("collation position changed identity")
	}
	other = f.predicate
	other.collationConflict = true
	if partialPredicateImpliedBy(other, active) {
		t.Fatal("indeterminate collation proved")
	}
	plan, err := sqltype.PlanCoercion(boolean, boolean, sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	coercion := coercionExpression{expressionInfo: f.predicate.expressionInfo, child: active, plan: plan, mode: sqltype.Explicit}
	sameCoercion := coercion
	sameCoercion.pos = parser.Position{Character: 2}
	if !partialPredicateImpliedBy(sameCoercion, coercion) {
		t.Fatal("exact coercion did not prove")
	}
	sameCoercion.mode = sqltype.Assignment
	if partialPredicateImpliedBy(sameCoercion, coercion) {
		t.Fatal("coercion mode mismatch proved")
	}
	// Exact comparisons do not derive inequalities or substitute parameters.
	integer := f.parent.columns[0].Usage
	n := columnExpression{expressionInfo: expressionInfo{usage: integer}, scope: 1, frame: 1, slot: 0}
	one, _ := sqltype.Integer(integer.Type, 1)
	two, _ := sqltype.Integer(integer.Type, 2)
	comparison, err := sqltype.BindComparisonPredicate(sqltype.CompareLess, integer.Type, integer.Type, boolean.Type)
	if err != nil {
		t.Fatal(err)
	}
	less := func(right BoundExpr) BoundExpr {
		return comparisonExpression{expressionInfo: expressionInfo{usage: boolean}, predicate: comparison, operator: "<", left: n, right: right}
	}
	p := less(literalExpression{expressionInfo: expressionInfo{usage: integer}, value: two})
	if partialPredicateImpliedBy(less(literalExpression{expressionInfo: expressionInfo{usage: integer}, value: one}), p) {
		t.Fatal("inequality proof widened")
	}
	unresolved := parameterExpression{expressionInfo: expressionInfo{usage: integer, properties: ExprProperties{ParameterDependent: true}}, number: 1}
	q := less(unresolved)
	qInfo := boundExpressionInfo(q)
	qInfo.properties.ParameterDependent = true
	q = comparisonExpression{expressionInfo: qInfo, predicate: comparison, operator: "<", left: n, right: unresolved}
	if partialPredicateImpliedBy(q, p) || !partialPredicateImpliedBy(and(p, param), p) {
		t.Fatal("parameter implication")
	}
	greater, err := sqltype.BindComparisonPredicate(sqltype.CompareGreater, integer.Type, integer.Type, boolean.Type)
	if err != nil {
		t.Fatal(err)
	}
	different := p.(comparisonExpression)
	different.predicate = greater
	if partialPredicateImpliedBy(different, p) {
		t.Fatal("different comparison provider proved")
	}
}

func partialMaskFixture(t *testing.T) (partialIndexFixture, *StatementScope, queryLeaf, *VisibilityView) {
	f := newPartialIndexFixture(t)
	scope := &StatementScope{owner: &transactionOwner{isolation: ReadCommitted}}
	leaf := queryLeaf{ref: ObjectRef{owner: f.e.owner, Epoch: f.e.state.epoch, Address: f.parent.address}, relation: f.parent, storage: f.store, partialCandidate: f.index}
	data := &visibilityData{catalog: f.e.CatalogRead(), frontier: f.e.commitSeq, committed: map[ObjectAddress][]rowVersion{f.parent.address: f.store.versions}, singleGeneration: f.store}
	view := newVisibilityView(data)
	t.Cleanup(view.Release)
	return f, scope, leaf, view
}

func TestPartialIndexCandidateMask(t *testing.T) {
	f, scope, leaf, view := partialMaskFixture(t)
	f.e.mu.Lock()
	mask := f.e.capturePartialMaskLocked(scope, leaf, view, queryRowMark{})
	f.e.mu.Unlock()
	if mask == nil {
		t.Fatal("coherent committed root did not attach")
	}
	auth := AuthorizationView{catalog: f.e.CatalogRead()}
	binder := expressionBinder{queryAuth: &auth}
	if binder.partialIndexCandidate(leaf.ref, f.predicate) != f.index {
		t.Fatal("bound caller did not select candidate")
	}
	calls := 0
	witness := partialEvaluationWitness{expressionInfo: f.predicate.expressionInfo, calls: &calls, value: sqltype.Boolean(f.predicate.usage.Type, true)}
	source := &querySource{view: view, ref: leaf.ref, committed: f.store.versions, scope: scope, partialMask: mask}
	scan := &sourceNode{querySchema: f.parent.columns, ref: leaf.ref}
	filter := &filterNode{querySchema: f.parent.columns, child: scan, predicate: witness}
	stage, err := openQueryNode(context.Background(), &queryState{source: source}, filter)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int
	for {
		i, ok, err := stage.nextOccurrence(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		ids = append(ids, stage.index(i))
	}
	if calls != 3 || !reflect.DeepEqual(ids, []int{0, 3, 4}) {
		t.Fatal("mask was not consulted before residual", calls, ids)
	}
	// A later generation/root change cannot mutate the retained selected mask.
	f.store.versions = append(f.store.versions, rowVersion{row: 6, version: 6})
	f.e.indexRoots[f.index.address] = newLogicalIndexRoot(f.index.sqlIndex, nil)
	if !partialMaskContains(mask, source.committed[0]) || partialMaskContains(mask, rowVersion{row: 1, version: 99}) {
		t.Fatal("retained identity mask changed")
	}
}

func TestPartialIndexCandidateFallback(t *testing.T) {
	for _, name := range []string{"private catalog", "own insert", "own update", "own delete", "historical", "frontier", "rewrite", "generation", "prefix", "vector", "ends", "definition", "catalog snapshot", "missing root", "epoch", "shape", "locking", "occurrence locking", "inherited locking", "absent candidate"} {
		t.Run(name, func(t *testing.T) {
			f, scope, leaf, view := partialMaskFixture(t)
			data := view.read()
			a := f.parent.address
			inherited := queryRowMark{}
			switch name {
			case "private catalog":
				scope.owner.catalog = &catalogChanges{}
			case "own insert":
				data.own = map[ObjectAddress][]rowVersion{a: {f.store.versions[1]}}
			case "own update":
				row := f.store.versions[0]
				row.base = row.version
				row.version = 100
				data.own = map[ObjectAddress][]rowVersion{a: {row}}
			case "own delete":
				data.deleted = map[ObjectAddress]*privateDeletionRoot{a: {}}
			case "historical":
				scope.owner.isolation = RepeatableRead
			case "frontier":
				f.e.commitSeq++
			case "rewrite":
				data.rewrittenFrontiers = map[ObjectAddress]CommitSeq{a: 0}
			case "generation":
				data.singleGeneration = &relationStorage{}
			case "prefix":
				data.committed[a] = data.committed[a][:1]
			case "vector":
				data.committed[a] = append([]rowVersion(nil), data.committed[a]...)
			case "ends":
				f.store.ends = &deletionRoot{}
			case "definition":
				copy := *f.index
				f.e.state.descriptors[f.index.address] = &copy
			case "catalog snapshot":
				data.catalog.state = cloneCatalog(f.e.state)
			case "missing root":
				delete(f.e.indexRoots, f.index.address)
			case "epoch":
				leaf.ref.Epoch++
			case "shape":
				copy := *f.parent
				copy.attributes = []int32{11, 20}
				f.e.state.descriptors[a] = &copy
			case "locking":
				scope.queryLocks = &queryLockContext{}
			case "occurrence locking":
				leaf.mark = queryRowMark{mode: rowUpdate}
			case "inherited locking":
				inherited = queryRowMark{mode: keyShare}
			case "absent candidate":
				leaf.partialCandidate = nil
			}
			f.e.mu.Lock()
			mask := f.e.capturePartialMaskLocked(scope, leaf, view, mergeQueryRowMark(inherited, leaf.mark))
			f.e.mu.Unlock()
			if mask != nil {
				t.Fatal("unsafe mask attached")
			}
		})
	}
	f := newPartialIndexFixture(t)
	auth := AuthorizationView{catalog: f.e.CatalogRead()}
	b := expressionBinder{queryAuth: &auth}
	param := parameterExpression{expressionInfo: expressionInfo{usage: f.predicate.usage, properties: ExprProperties{ParameterDependent: true}}, number: 1}
	ref := ObjectRef{owner: f.e.owner, Epoch: f.e.state.epoch, Address: f.parent.address}
	if b.partialIndexCandidate(ref, param) != nil {
		t.Fatal("unresolved parameter selected root")
	}
}

func TestPartialIndexPredicateLifecycle(t *testing.T) {
	f := newPartialIndexFixture(t)
	// Predicate-only dependency survives original-tree simplification.
	original := *f.index.sqlIndex.predicate
	fixed := literalExpression{expressionInfo: f.predicate.expressionInfo, value: sqltype.Boolean(f.predicate.usage.Type, false)}
	original.execution = fixed
	def := *f.index.sqlIndex
	def.predicate = &original
	index := *f.index
	index.sqlIndex = &def
	parent := *f.parent
	parent.attributes = []int32{10}
	parent.columns = parent.columns[:1]
	removed, err := evolveIndex(&index, f.parent, &parent, nil)
	if err != nil || removed != nil {
		t.Fatal("predicate-only drop retained index", removed, err)
	}
	// An unrelated earlier slot must detach and remap BOTH predicate trees.
	parent = *f.parent
	parent.attributes = []int32{5, 10, 20}
	parent.columns = append([]ColumnDescriptor{f.parent.columns[0]}, f.parent.columns...)
	column := f.predicate
	column.slot = 2
	p := *f.index.sqlIndex.predicate
	p.attributes = parent.attributes
	p.definition, p.execution = column, column
	def = *f.index.sqlIndex
	def.predicate = &p
	index = *f.index
	index.sqlIndex = &def
	next, err := evolveIndex(&index, &parent, f.parent, nil)
	if err != nil || next == nil {
		t.Fatal(err)
	}
	if next.sqlIndex.predicate == &p || next.sqlIndex.predicate.definition.(columnExpression).slot != 1 || next.sqlIndex.predicate.execution.(columnExpression).slot != 1 || p.definition.(columnExpression).slot != 2 {
		t.Fatal("predicate slot remap mutated or missed trees")
	}
}

func TestPartialIndexPredicateReplacement(t *testing.T) {
	f := newPartialIndexFixture(t)
	old := f.e.indexRoots[f.index.address]
	destination := &relationStorage{versions: append([]rowVersion(nil), f.store.versions...)}
	f.e.nextRow, f.e.nextVersion = 100, 200
	roots, rows, versions, err := f.e.prepareRemintedStorage(context.Background(), f.e.state, map[ObjectAddress]*relationStorage{f.parent.address: destination})
	if err != nil || rows != 105 || versions != 205 {
		t.Fatal(rows, versions, err)
	}
	partialWantMembership(t, roots[f.index.address], map[RowID]VersionID{101: 201, 104: 204, 105: 205})
	partialWantMembership(t, old, map[RowID]VersionID{1: 1, 4: 4, 5: 5})
	if roots[f.index.address] == old || f.store.versions[0].row != 1 || f.e.nextRow != 100 {
		t.Fatal("replacement transplanted/mutated source")
	}
	target := New()
	next := cloneCatalog(f.e.state)
	next.epoch = target.state.epoch + 1
	if err := target.remapCatalogExpressions(next, nil); err != nil {
		t.Fatal(err)
	}
	remapped := next.descriptors[f.index.address].(*relationDescriptor).sqlIndex.predicate
	if remapped == f.index.sqlIndex.predicate {
		t.Fatal("replacement reused source predicate")
	}
	for _, node := range []BoundExpr{remapped.definition, remapped.execution} {
		origin := node.(columnExpression).origin
		if origin.owner != target.owner || origin.Epoch != target.state.epoch+1 {
			t.Fatal("foreign origin retained", origin)
		}
	}
	if remapped.dependencies[0].owner != target.owner || remapped.dependencies[0].Epoch != target.state.epoch+1 || f.index.sqlIndex.predicate.dependencies[0].owner != f.e.owner {
		t.Fatal("dependency authority was transplanted or source mutated")
	}
}
