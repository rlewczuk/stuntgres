package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

type partialIndexFixture struct {
	e             *Engine
	parent, index *catalog.Relation
	predicate     plan.ColumnExpr
	store         *storage.Relation
}

func newPartialIndexFixture(t testing.TB) partialIndexFixture {
	t.Helper()
	e := New()
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
	parent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "t", ObjectRevision: 1, RelationKind: catalog.TableRelation, Attributes: []int32{10, 20}, ColumnMeta: catalog.ColumnMetadata{HighWater: 20}, Columns: []catalog.Column{{Name: "k", Usage: integer.Usage()}, {Name: "active", Usage: boolean.Usage()}}, Index: map[string]int{"k": 0, "active": 1}}
	ref := e.catalog.Domain.Ref(e.catalog.state.Epoch(), parent.Address())
	ref.Address.SubID = 20
	column := plan.ColumnExpr{Info: expr.Info{Usage: boolean.Usage(), Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1, Slot: 1, Origin: ref}
	predicate := &catalog.IndexPredicate{Definition: column, Execution: column, Attributes: parent.Attributes, Dependencies: []ids.ObjectRef{ref}}
	class, _ := sqltype.DefaultIndexClass(integer)
	index := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20001}, ObjectNamespace: parent.Namespace(), ObjectName: "pi", ObjectRevision: 1, RelationKind: catalog.IndexRelation, Columns: []catalog.Column{parent.Columns[0]}, Attributes: []int32{1}, ColumnMeta: catalog.ColumnMetadata{HighWater: 1}, Index: map[string]int{"k": 0}, SQLIndex: &catalog.IndexDefinition{Parent: parent.Address(), Method: catalog.BtreeMethod, Ready: true, Valid: true, Live: true, Keys: []catalog.IndexKey{{Attribute: 10, Class: class}}, Predicate: predicate}}
	store := &storage.Relation{}
	for i, k := range []int64{10, 20, 30, 0, 10} {
		value, _ := sqltype.Integer(integer, k)
		if i == 3 {
			value = sqltype.Null(integer)
		}
		active := sqltype.Boolean(boolean, i == 0 || i == 3 || i == 4)
		if i == 2 {
			active = sqltype.Null(boolean)
		}
		store.Versions = append(store.Versions, storage.RowVersion{Row: ids.RowID(i + 1), Version: ids.VersionID(i + 1), Values: []sqltype.Value{value, active}})
	}
	e.catalog.state.SetDescriptor(parent.Address(), parent)
	e.catalog.state.SetDescriptor(index.Address(), index)
	e.mu.Lock()
	e.storage.InstallRelationLocked(parent.Address(), store)
	e.mu.Unlock()
	root, err := buildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, parent, index.SQLIndex, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	e.storage.ReplaceIndexRootsLocked(map[ids.ObjectAddress]*storage.IndexRoot{index.Address(): root})
	e.mu.Unlock()
	return partialIndexFixture{e, parent, index, column, store}
}

func partialWantMembership(t *testing.T, root *storage.IndexRoot, want map[ids.RowID]ids.VersionID) {
	t.Helper()
	if !reflect.DeepEqual(root.Membership, want) || len(root.Entries) != len(want) {
		t.Fatal("wrong membership", root.Membership, want, root.Entries)
	}
	for _, entry := range root.Entries {
		if root.Membership[entry.Row] != entry.Version {
			t.Fatal("entry/lookup disagreement", entry)
		}
	}
}

func TestPartialIndexPredicateTruth(t *testing.T) {
	f := newPartialIndexFixture(t)
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	var preparation, absent indexExecutionPreparation
	for i, row := range f.store.Versions {
		got, err := preparation.predicateMatches(&evaluation, f.parent, f.index.SQLIndex, row.Values, nil, nil)
		if err != nil || got != (i == 0 || i == 3 || i == 4) {
			t.Fatal(i, got, err)
		}
		got, err = absent.predicateMatches(&evaluation, f.parent, &catalog.IndexDefinition{}, row.Values, nil, nil)
		if err != nil || !got {
			t.Fatal("absent predicate", got, err)
		}
	}
	boolean := f.predicate.Usage.Type
	for _, value := range []sqltype.Value{sqltype.Boolean(boolean, true), sqltype.Boolean(boolean, false), sqltype.Null(boolean)} {
		node := plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: value}
		p := &catalog.IndexPredicate{Definition: node, Execution: node}
		preparation := indexExecutionPreparation{}
		got, err := preparation.predicateMatches(&evaluation, f.parent, &catalog.IndexDefinition{Predicate: p}, nil, nil, nil)
		truth, _ := plan.TruthOf(value)
		if err != nil || got != (truth == plan.True) {
			t.Fatal(value, got, err)
		}
	}
}

func TestPartialIndexRootMembership(t *testing.T) {
	f := newPartialIndexFixture(t)
	root := f.e.storage.IndexRoots[f.index.Address()]
	partialWantMembership(t, root, map[ids.RowID]ids.VersionID{1: 1, 4: 4, 5: 5})
	if got := btreeKeys(t, root); !reflect.DeepEqual(got, []string{"10", "10", "NULL"}) {
		t.Fatal(got)
	}
	if len(f.store.Versions) != 5 {
		t.Fatal("build changed table")
	}
	ordinary := *f.index.SQLIndex
	ordinary.Predicate = nil
	all, err := buildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, f.parent, &ordinary, f.store, nil)
	if err != nil || len(all.Entries) != 5 || all.Membership != nil {
		t.Fatal("ordinary path", all, err)
	}
	for _, tc := range []struct {
		value sqltype.Value
		count int
	}{{sqltype.Boolean(f.predicate.Usage.Type, true), 5}, {sqltype.Boolean(f.predicate.Usage.Type, false), 0}, {sqltype.Null(f.predicate.Usage.Type), 0}} {
		node := plan.LiteralExpr{Info: expr.Info{Usage: f.predicate.Usage}, Value: tc.value}
		def := *f.index.SQLIndex
		def.Predicate = &catalog.IndexPredicate{Definition: node, Execution: node}
		root, err := buildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, f.parent, &def, f.store, nil)
		if err != nil || len(root.Entries) != tc.count || len(root.Membership) != tc.count {
			t.Fatal("constant root", tc, root, err)
		}
	}
}

func TestPartialIndexMembershipTransitions(t *testing.T) {
	f := newPartialIndexFixture(t)
	old := f.e.storage.IndexRoots[f.index.Address()]
	boolean := f.predicate.Usage.Type
	write := func(id ids.RowID, version, base ids.VersionID, active sqltype.Value) storage.RowVersion {
		row := f.store.Versions[id-1]
		row.Version, row.Base = version, base
		row.Values = append([]sqltype.Value(nil), row.Values...)
		row.Values[1] = active
		return row
	}
	p := &rowPublication{writes: []storage.RowVersion{
		write(1, 6, 1, sqltype.Boolean(boolean, false)),
		write(2, 7, 2, sqltype.Boolean(boolean, true)),
		write(3, 8, 3, sqltype.Boolean(boolean, true)),
		write(4, 9, 4, sqltype.Null(boolean)),
		write(5, 10, 5, sqltype.Boolean(boolean, true)),
	}}
	next, err := mergeIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, f.parent, f.index, old.Entries, p, false, true, false, "", sqltype.OutputEnvironment{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	partialWantMembership(t, next, map[ids.RowID]ids.VersionID{2: 7, 3: 8, 5: 10})
	partialWantMembership(t, old, map[ids.RowID]ids.VersionID{1: 1, 4: 4, 5: 5})
	p = &rowPublication{deletion: &visibility.PrivateDeletes{Rows: map[ids.RowID]visibility.DeleteCandidate{2: {Version: 7}, 1: {Version: 6}}}}
	next, err = mergeIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, f.parent, f.index, next.Entries, p, false, true, false, "", sqltype.OutputEnvironment{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	partialWantMembership(t, next, map[ids.RowID]ids.VersionID{3: 8, 5: 10})
	// Excluded successors and own-deleted additions must never resurrect identities.
	p = &rowPublication{writes: []storage.RowVersion{write(3, 11, 8, sqltype.Boolean(boolean, false)), write(2, 12, 0, sqltype.Boolean(boolean, true))}, deletion: &visibility.PrivateDeletes{Rows: map[ids.RowID]visibility.DeleteCandidate{2: {Version: 12, Own: true}}}}
	next, err = mergeIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, f.parent, f.index, next.Entries, p, false, true, false, "", sqltype.OutputEnvironment{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	partialWantMembership(t, next, map[ids.RowID]ids.VersionID{5: 10})
}

// Test-only execution witness: excluded rows must not call the key provider.
type partialEvaluationWitness struct {
	expr.Info
	calls   *int
	failure error
	value   sqltype.Value
}

func (w partialEvaluationWitness) Eval(expr.Context) (sqltype.Value, error) {
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
	scope := &StatementScope{op: op, txRef: (*ids.Domain)(nil).Tx(ids.SessionToken{}, 0, f.e.catalog.state.Epoch(), nil, 0)}
	calls := 0
	failure := errors.New("qualifying key")
	key := partialEvaluationWitness{Info: expr.Info{Usage: f.parent.Columns[0].Usage}, calls: &calls, failure: failure}
	def := *f.index.SQLIndex
	def.Keys = []catalog.IndexKey{{Class: f.index.SQLIndex.Keys[0].Class, Derived: &catalog.DerivedIndexKey{Definition: key, Execution: key}}}
	idx := *f.index
	idx.SQLIndex = &def
	f.e.catalog.state.SetDescriptor(idx.Address(), &idx)
	scope.stage.rows = map[ids.ObjectAddress][]storage.RowVersion{f.parent.Address(): {f.store.Versions[1], f.store.Versions[2]}}
	if err := scope.validatePrivateIndexExpressions(nil, nil); err != nil || calls != 0 {
		t.Fatal("excluded keys evaluated", calls, err)
	}
	excluded := &storage.Relation{Versions: scope.stage.rows[f.parent.Address()]}
	built, err := buildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, f.parent, &def, excluded, nil)
	if err != nil || calls != 0 || len(built.Entries) != 0 {
		t.Fatal("build evaluated excluded keys", calls, err)
	}
	merged, err := mergeIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, f.parent, &idx, built.Entries, &rowPublication{writes: excluded.Versions}, false, true, false, "", sqltype.OutputEnvironment{}, nil)
	if err != nil || calls != 0 || merged != nil {
		t.Fatal("excluded writes changed root or evaluated keys", merged, calls, err)
	}
	scope.stage.rows[f.parent.Address()] = []storage.RowVersion{f.store.Versions[0]}
	if err := scope.validatePrivateIndexExpressions(nil, nil); !errors.Is(err, failure) || calls != 1 {
		t.Fatal("qualifying key not vetoed", calls, err)
	}
	// Simple keys still preflight predicate failures, rather than waiting for COMMIT.
	calls = 0
	predicate := partialEvaluationWitness{Info: f.predicate.Info, calls: &calls, failure: failure}
	def.Keys = f.index.SQLIndex.Keys
	def.Predicate = &catalog.IndexPredicate{Definition: predicate, Execution: predicate}
	if err := scope.validatePrivateIndexExpressions(nil, nil); !errors.Is(err, failure) || calls != 1 {
		t.Fatal("simple-key predicate not preflighted", calls, err)
	}
	calls = 0
	if _, err := buildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, f.parent, &def, f.store, nil); !errors.Is(err, failure) || calls != 1 {
		t.Fatal("build predicate error", calls, err)
	}
	calls = 0
	if _, err := mergeIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, f.parent, &idx, f.e.storage.IndexRoots[f.index.Address()].Entries, &rowPublication{writes: scope.stage.rows[f.parent.Address()]}, false, true, false, "", sqltype.OutputEnvironment{}, nil); !errors.Is(err, failure) || calls != 1 {
		t.Fatal("merge predicate error", calls, err)
	}
}

func TestPartialIndexPredicateCancellation(t *testing.T) {
	f := newPartialIndexFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	for _, p := range []*catalog.IndexPredicate{nil, f.index.SQLIndex.Predicate} {
		preparation := indexExecutionPreparation{}
		if _, err := preparation.predicateMatches(&evaluation, f.parent, &catalog.IndexDefinition{Predicate: p}, nil, nil, nil); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	root := f.e.storage.IndexRoots[f.index.Address()]
	if _, err := buildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, f.parent, f.index.SQLIndex, f.store, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := mergeIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, f.parent, f.index, root.Entries, &rowPublication{}, false, true, false, "", sqltype.OutputEnvironment{}, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	partialWantMembership(t, root, map[ids.RowID]ids.VersionID{1: 1, 4: 4, 5: 5})
}

func TestPartialIndexImplication(t *testing.T) {
	f := newPartialIndexFixture(t)
	active := expr.Expr(f.predicate)
	boolean := f.predicate.Usage
	and := func(a, b expr.Expr) expr.Expr {
		return plan.LogicalExpr{Info: expr.Info{Usage: boolean}, Op: parser.LogicalAnd, Left: a, Right: b}
	}
	or := func(a, b expr.Expr) expr.Expr {
		return plan.LogicalExpr{Info: expr.Info{Usage: boolean}, Op: parser.LogicalOr, Left: a, Right: b}
	}
	param := plan.ParameterExpr{Info: expr.Info{Usage: boolean, Property: expr.Properties{ParameterDependent: true}}, Number: 1}
	fixed := plan.LiteralExpr{Info: expr.Info{Usage: boolean}, Value: sqltype.Boolean(boolean.Type, true)}
	other := f.predicate
	other.Origin.Address.SubID++
	for _, tc := range []struct {
		q, p expr.Expr
		want bool
	}{
		{active, active, true}, {and(active, param), active, true}, {param, active, false}, {or(active, param), active, false},
		{and(fixed, and(active, param)), and(active, fixed), true}, {active, and(active, fixed), false},
		{other, active, false}, {active, nil, true}, {nil, active, false}, {param, param, false},
		{or(active, fixed), or(active, fixed), true},
	} {
		if got := analyze.PartialPredicateImpliedBy(tc.q, tc.p); got != tc.want {
			t.Fatal(tc, got)
		}
	}
	other = f.predicate
	other.Pos = parser.Position{Character: 999}
	if !analyze.PartialPredicateImpliedBy(other, active) {
		t.Fatal("position changed identity")
	}
	other = f.predicate
	other.Usage = f.parent.Columns[0].Usage
	if analyze.PartialPredicateImpliedBy(other, active) {
		t.Fatal("type mismatch proved")
	}
	unknown := partialEvaluationWitness{Info: f.predicate.Info}
	if analyze.PartialPredicateImpliedBy(unknown, unknown) {
		t.Fatal("unknown node proved")
	}
	other = f.predicate
	other.CollationExplicit = parser.Position{Character: 1}
	if analyze.PartialPredicateImpliedBy(other, active) {
		t.Fatal("explicit collation provenance mismatch proved")
	}
	explicit := other
	other.CollationExplicit.Character = 2
	if !analyze.PartialPredicateImpliedBy(other, explicit) {
		t.Fatal("collation position changed identity")
	}
	other = f.predicate
	other.CollationConflict = true
	if analyze.PartialPredicateImpliedBy(other, active) {
		t.Fatal("indeterminate collation proved")
	}
	coercionPlan, err := sqltype.PlanCoercion(boolean, boolean, sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	coercion := plan.CoercionExpr{Info: f.predicate.Info, Child: active, Plan: coercionPlan, Mode: sqltype.Explicit}
	sameCoercion := coercion
	sameCoercion.Pos = parser.Position{Character: 2}
	if !analyze.PartialPredicateImpliedBy(sameCoercion, coercion) {
		t.Fatal("exact coercion did not prove")
	}
	sameCoercion.Mode = sqltype.Assignment
	if analyze.PartialPredicateImpliedBy(sameCoercion, coercion) {
		t.Fatal("coercion mode mismatch proved")
	}
	// Exact comparisons do not derive inequalities or substitute parameters.
	integer := f.parent.Columns[0].Usage
	n := plan.ColumnExpr{Info: expr.Info{Usage: integer}, Scope: 1, Frame: 1, Slot: 0}
	one, _ := sqltype.Integer(integer.Type, 1)
	two, _ := sqltype.Integer(integer.Type, 2)
	comparison, err := sqltype.BindComparisonPredicate(sqltype.CompareLess, integer.Type, integer.Type, boolean.Type, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	less := func(right expr.Expr) expr.Expr {
		return plan.ComparisonExpr{Info: expr.Info{Usage: boolean}, Predicate: comparison, Operator: "<", Left: n, Right: right}
	}
	p := less(plan.LiteralExpr{Info: expr.Info{Usage: integer}, Value: two})
	if analyze.PartialPredicateImpliedBy(less(plan.LiteralExpr{Info: expr.Info{Usage: integer}, Value: one}), p) {
		t.Fatal("inequality proof widened")
	}
	unresolved := plan.ParameterExpr{Info: expr.Info{Usage: integer, Property: expr.Properties{ParameterDependent: true}}, Number: 1}
	q := less(unresolved)
	qInfo := expr.InfoOf(q)
	qInfo.Property.ParameterDependent = true
	q = plan.ComparisonExpr{Info: qInfo, Predicate: comparison, Operator: "<", Left: n, Right: unresolved}
	if analyze.PartialPredicateImpliedBy(q, p) || !analyze.PartialPredicateImpliedBy(and(p, param), p) {
		t.Fatal("parameter implication")
	}
	greater, err := sqltype.BindComparisonPredicate(sqltype.CompareGreater, integer.Type, integer.Type, boolean.Type, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	different := p.(plan.ComparisonExpr)
	different.Predicate = greater
	if analyze.PartialPredicateImpliedBy(different, p) {
		t.Fatal("different comparison provider proved")
	}
}

func partialMaskFixture(t *testing.T) (partialIndexFixture, *StatementScope, exec.Leaf, *visibility.View) {
	f := newPartialIndexFixture(t)
	scope := &StatementScope{owner: &transactionOwner{isolation: ReadCommitted}}
	leaf := exec.Leaf{Ref: f.e.catalog.Domain.Ref(f.e.catalog.state.Epoch(), f.parent.Address()), Relation: f.parent, Storage: f.store, PartialCandidate: f.index}
	data := &visibility.Data{Catalog: f.e.CatalogRead(), Frontier: f.e.visibility.CommitSeq(), Committed: map[ids.ObjectAddress][]storage.RowVersion{f.parent.Address(): f.store.Versions}, SingleGeneration: f.store}
	view := visibility.NewView(data)
	t.Cleanup(view.Release)
	return f, scope, leaf, view
}

func TestPartialIndexCandidateMask(t *testing.T) {
	f, scope, leaf, view := partialMaskFixture(t)
	f.e.mu.Lock()
	mask := f.e.capturePartialMaskLocked(scope, leaf, view, plan.RowMark{})
	f.e.mu.Unlock()
	if mask == nil {
		t.Fatal("coherent committed root did not attach")
	}
	auth := AuthorizationView{View: access.View{Catalog: f.e.CatalogRead()}}
	binder := analyze.Binder{Query: analyze.QueryScopeState{Auth: &auth}}
	if binder.Query.PartialIndexCandidate(leaf.Ref, f.predicate) != f.index {
		t.Fatal("bound caller did not select candidate")
	}
	calls := 0
	witness := partialEvaluationWitness{Info: f.predicate.Info, calls: &calls, value: sqltype.Boolean(f.predicate.Usage.Type, true)}
	source := &exec.Source{View: view, Ref: leaf.Ref, Committed: f.store.Versions, Host: hostOf(scope), PartialMask: mask}
	scan := &plan.SourceNode{QuerySchema: f.parent.Columns, Ref: leaf.Ref}
	filter := &plan.FilterNode{QuerySchema: f.parent.Columns, Child: scan, Predicate: witness}
	stage, err := exec.OpenNode(context.Background(), &exec.State{Source: source}, filter)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int
	for {
		i, ok, err := stage.NextOccurrence(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		ids = append(ids, stage.Index(i))
	}
	if calls != 3 || !reflect.DeepEqual(ids, []int{0, 3, 4}) {
		t.Fatal("mask was not consulted before residual", calls, ids)
	}
	// A later generation/root change cannot mutate the retained selected mask.
	f.store.Versions = append(f.store.Versions, storage.RowVersion{Row: 6, Version: 6})
	f.e.mu.Lock()
	f.e.storage.InstallIndexRootLocked(f.index.Address(), newLogicalIndexRoot(f.index.SQLIndex, nil))
	f.e.mu.Unlock()
	if !exec.PartialMaskContains(mask, source.Committed[0]) || exec.PartialMaskContains(mask, storage.RowVersion{Row: 1, Version: 99}) {
		t.Fatal("retained identity mask changed")
	}
}

func TestPartialIndexCandidateFallback(t *testing.T) {
	for _, name := range []string{"private catalog", "own insert", "own update", "own delete", "historical", "frontier", "rewrite", "generation", "prefix", "vector", "ends", "definition", "catalog snapshot", "missing root", "epoch", "shape", "locking", "occurrence locking", "inherited locking", "absent candidate"} {
		t.Run(name, func(t *testing.T) {
			f, scope, leaf, view := partialMaskFixture(t)
			data := view.Data()
			a := f.parent.Address()
			inherited := plan.RowMark{}
			switch name {
			case "private catalog":
				scope.owner.catalog = &catalogChanges{}
			case "own insert":
				data.Own = map[ids.ObjectAddress][]storage.RowVersion{a: {f.store.Versions[1]}}
			case "own update":
				row := f.store.Versions[0]
				row.Base = row.Version
				row.Version = 100
				data.Own = map[ids.ObjectAddress][]storage.RowVersion{a: {row}}
			case "own delete":
				data.Deleted = map[ids.ObjectAddress]*visibility.PrivateDeletes{a: {}}
			case "historical":
				scope.owner.isolation = RepeatableRead
			case "frontier":
				f.e.visibility.AdvanceCommitSeqLocked()
			case "rewrite":
				data.RewrittenFrontiers = map[ids.ObjectAddress]ids.CommitSeq{a: 0}
			case "generation":
				data.SingleGeneration = &storage.Relation{}
			case "prefix":
				data.Committed[a] = data.Committed[a][:1]
			case "vector":
				data.Committed[a] = append([]storage.RowVersion(nil), data.Committed[a]...)
			case "ends":
				f.store.Ends = &storage.DeletionRoot{}
			case "definition":
				copy := *f.index
				f.e.catalog.state.SetDescriptor(f.index.Address(), &copy)
			case "catalog snapshot":
				data.Catalog = data.Catalog.WithState(catalog.CloneState(f.e.catalog.state))
			case "missing root":
				f.e.mu.Lock()
				f.e.storage.RemoveIndexRootLocked(f.index.Address())
				f.e.mu.Unlock()
			case "epoch":
				leaf.Ref.Epoch++
			case "shape":
				copy := *f.parent
				copy.Attributes = []int32{11, 20}
				f.e.catalog.state.SetDescriptor(a, &copy)
			case "locking":
				scope.queryLocks = &exec.LockContext{}
			case "occurrence locking":
				leaf.Mark = plan.RowMark{Mode: lock.ForUpdate}
			case "inherited locking":
				inherited = plan.RowMark{Mode: lock.ForKeyShare}
			case "absent candidate":
				leaf.PartialCandidate = nil
			}
			f.e.mu.Lock()
			mask := f.e.capturePartialMaskLocked(scope, leaf, view, exec.MergeQueryRowMark(inherited, leaf.Mark))
			f.e.mu.Unlock()
			if mask != nil {
				t.Fatal("unsafe mask attached")
			}
		})
	}
	f := newPartialIndexFixture(t)
	auth := AuthorizationView{View: access.View{Catalog: f.e.CatalogRead()}}
	b := analyze.Binder{Query: analyze.QueryScopeState{Auth: &auth}}
	param := plan.ParameterExpr{Info: expr.Info{Usage: f.predicate.Usage, Property: expr.Properties{ParameterDependent: true}}, Number: 1}
	ref := f.e.catalog.Domain.Ref(f.e.catalog.state.Epoch(), f.parent.Address())
	if b.Query.PartialIndexCandidate(ref, param) != nil {
		t.Fatal("unresolved parameter selected root")
	}
}

func TestPartialIndexPredicateLifecycle(t *testing.T) {
	f := newPartialIndexFixture(t)
	// Predicate-only dependency survives original-tree simplification.
	original := *f.index.SQLIndex.Predicate
	fixed := plan.LiteralExpr{Info: f.predicate.Info, Value: sqltype.Boolean(f.predicate.Usage.Type, false)}
	original.Execution = fixed
	def := *f.index.SQLIndex
	def.Predicate = &original
	index := *f.index
	index.SQLIndex = &def
	parent := *f.parent
	parent.Attributes = []int32{10}
	parent.Columns = parent.Columns[:1]
	removed, err := evolveIndex(&index, f.parent, &parent, nil)
	if err != nil || removed != nil {
		t.Fatal("predicate-only drop retained index", removed, err)
	}
	// An unrelated earlier slot must detach and remap BOTH predicate trees.
	parent = *f.parent
	parent.Attributes = []int32{5, 10, 20}
	parent.Columns = append([]catalog.Column{f.parent.Columns[0]}, f.parent.Columns...)
	column := f.predicate
	column.Slot = 2
	p := *f.index.SQLIndex.Predicate
	p.Attributes = parent.Attributes
	p.Definition, p.Execution = column, column
	def = *f.index.SQLIndex
	def.Predicate = &p
	index = *f.index
	index.SQLIndex = &def
	next, err := evolveIndex(&index, &parent, f.parent, nil)
	if err != nil || next == nil {
		t.Fatal(err)
	}
	if next.SQLIndex.Predicate == &p || next.SQLIndex.Predicate.Definition.(plan.ColumnExpr).Slot != 1 || next.SQLIndex.Predicate.Execution.(plan.ColumnExpr).Slot != 1 || p.Definition.(plan.ColumnExpr).Slot != 2 {
		t.Fatal("predicate slot remap mutated or missed trees")
	}
}

func TestPartialIndexPredicateReplacement(t *testing.T) {
	f := newPartialIndexFixture(t)
	old := f.e.storage.IndexRoots[f.index.Address()]
	destination := &storage.Relation{Versions: append([]storage.RowVersion(nil), f.store.Versions...)}
	f.e.storage.SetFrontierLocked(100, 200)
	snapshot, err := f.e.captureIndexSnapshot(context.Background(), f.e.CatalogRead(), f.e.storage.Relations, f.e.storage.IndexRoots, f.e.visibility.CommitSeq())
	if err != nil {
		t.Fatal(err)
	}
	roots, rows, versions, err := f.e.prepareRemintedStorage(context.Background(), f.e.catalog.state, map[ids.ObjectAddress]*storage.Relation{f.parent.Address(): destination}, snapshot)
	if err != nil || rows != 105 || versions != 205 {
		t.Fatal(rows, versions, err)
	}
	partialWantMembership(t, roots[f.index.Address()], map[ids.RowID]ids.VersionID{101: 201, 104: 204, 105: 205})
	partialWantMembership(t, old, map[ids.RowID]ids.VersionID{1: 1, 4: 4, 5: 5})
	if roots[f.index.Address()] == old || f.store.Versions[0].Row != 1 || f.e.storage.RowFrontier() != 100 {
		t.Fatal("replacement transplanted/mutated source")
	}
	target := New()
	next := catalog.CloneState(f.e.catalog.state)
	next.SetEpoch(target.catalog.state.Epoch() + 1)
	if err := target.remapCatalogExpressions(next, nil); err != nil {
		t.Fatal(err)
	}
	remapped := next.MustRelation(f.index.Address()).SQLIndex.Predicate
	if remapped == f.index.SQLIndex.Predicate {
		t.Fatal("replacement reused source predicate")
	}
	for _, node := range []expr.Expr{remapped.Definition, remapped.Execution} {
		origin := node.(plan.ColumnExpr).Origin
		if origin.Domain() != target.catalog.Domain || origin.Epoch != target.catalog.state.Epoch()+1 {
			t.Fatal("foreign origin retained", origin)
		}
	}
	if remapped.Dependencies[0].Domain() != target.catalog.Domain || remapped.Dependencies[0].Epoch != target.catalog.state.Epoch()+1 || f.index.SQLIndex.Predicate.Dependencies[0].Domain() != f.e.catalog.Domain {
		t.Fatal("dependency authority was transplanted or source mutated")
	}
}
