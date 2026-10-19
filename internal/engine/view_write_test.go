package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

type viewWriteFixture struct {
	e                *Engine
	base, view       *catalog.Relation
	baseRef, viewRef ids.ObjectRef
	integer, boolean *sqltype.Descriptor
}

// No SQL/API witnesses live here. The fixture supplies immutable analyzed nodes
// and physical versions directly to the production rewrite/publication seams.
func viewWriteMechanics(t *testing.T) viewWriteFixture {
	t.Helper()
	e := New()
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	owner := e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	base := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}, ObjectNamespace: ns, ObjectName: "base", ObjectRevision: 1, Owner: owner, RelationKind: catalog.TableRelation, Attributes: []int32{7, 11, 19}, Columns: []catalog.Column{{Name: "id", Usage: integer.Usage(), Nullable: true}, {Name: "a", Usage: integer.Usage(), Nullable: true}, {Name: "hidden", Usage: integer.Usage(), Nullable: true}}, Index: map[string]int{"id": 0, "a": 1, "hidden": 2}, ColumnMeta: catalog.ColumnMetadata{HighWater: 19}}
	baseRef := e.catalog.Domain.Ref(e.catalog.state.Epoch(), base.Address())
	column := func(slot int) expr.Expr {
		ref := baseRef
		ref.Address.SubID = base.Attributes[slot]
		return columnExpression{Info: expr.Info{Usage: integer.Usage(), Property: expr.Properties{RowDependent: true}}, origin: ref, scope: 1, frame: 1, slot: slot}
	}
	view := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20001}, ObjectNamespace: ns, ObjectName: "logical", ObjectRevision: 1, Owner: owner, RelationKind: catalog.ViewRelation, Attributes: []int32{1, 2}, ColumnMeta: catalog.ColumnMetadata{HighWater: 2}, Columns: []catalog.Column{{Name: "x", Usage: integer.Usage(), Nullable: true}, {Name: "key", Usage: integer.Usage(), Nullable: true}}, Index: map[string]int{"x": 0, "key": 1}}
	syntax := &parser.Select{From: &parser.TableReference{}}
	project := &projectNode{querySchema: querySchema(view.Columns), frame: 1, syntax: syntax, child: &sourceNode{querySchema: querySchema(base.Columns), ref: baseRef, occurrence: 0}, expressions: []expr.Expr{column(1), column(0)}}
	view.View = &viewDefinition{syntax: syntax, query: &BoundQuery{Root: project}, sources: []viewLeaf{{ref: baseRef, attributes: []int32{7, 11, 19}, reads: []int32{7, 11}}}}
	viewRef := e.catalog.Domain.Ref(e.catalog.state.Epoch(), view.Address())
	e.catalog.state.SetDescriptor(base.Address(), base)
	e.catalog.state.SetDescriptor(view.Address(), view)
	e.catalog.state.BindName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: base.Name()}, base.Address())
	e.catalog.state.BindName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: view.Name()}, view.Address())
	value := func(n int64) sqltype.Value {
		v, err := sqltype.Integer(integer, n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	e.storage.Relations[base.Address()] = &storage.Relation{Generation: 1, Versions: []storage.RowVersion{{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{value(1), value(10), value(42)}}, {Row: 2, Version: 2, Born: 1, Values: []sqltype.Value{value(2), value(20), value(42)}}}}
	e.visibility.commitSeq = 1
	e.storage.SetFrontierLocked(2, 2)
	return viewWriteFixture{e, base, view, baseRef, viewRef, integer, boolean}
}

func (f viewWriteFixture) authorization(t *testing.T, op *Operation) AuthorizationView {
	t.Helper()
	f.e.mu.Lock()
	defer f.e.mu.Unlock()
	v, err := f.e.statementAuthorizationLocked(op, &parser.Update{})
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func (f viewWriteFixture) scope(t *testing.T, explicit ...bool) (*StatementScope, *Operation) {
	t.Helper()
	session := sessionStart(t, f.e, nil)
	if len(explicit) != 0 && explicit[0] {
		transactionBegin(t, session)
	}
	op := transactionOperation(t, session)
	scope, err := f.e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.gate(&parser.Update{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !scope.finished {
			_ = scope.Finish(errors.New("mechanics cleanup"))
		}
	})
	w := &viewWrite{base: f.base, ref: f.baseRef, layers: []viewWriteLayer{{relation: f.view, ref: f.viewRef, source: f.base, sourceRef: f.baseRef}}}
	scope.lockedRef, scope.lockedDescriptor = f.viewRef, f.view
	if err := scope.acquireViewWriteTarget(w); err != nil {
		t.Fatal(err)
	}
	return scope, op
}

func TestViewWriteMapping(t *testing.T) {
	f := viewWriteMechanics(t)
	scope, op := f.scope(t)
	auth := f.authorization(t, op)
	b := binding{relation: f.view, ref: f.viewRef, targets: []int{0}, update: &boundUpdate{assignments: []expr.Expr{literalExpression{Info: expr.Info{Usage: f.integer.Usage()}, value: sqltype.Null(f.integer)}}}}
	if err := f.e.bindViewWrite(&b, auth, &parser.Update{}); err != nil {
		t.Fatal(err)
	}
	if b.relation != f.view || b.ref != f.viewRef || b.targets[0] != 1 || b.physicalBinding().ref != f.baseRef {
		t.Fatal("logical/physical authority conflated", b.targets)
	}
	// Creation slots survive current dense compaction and column renames.
	compact := *f.base
	compact.Attributes = []int32{11, 19}
	compact.Columns = []catalog.Column{f.base.Columns[1], f.base.Columns[2]}
	compact.Columns[0].Name = "renamed"
	slot, err := stableViewSlot(&compact, viewOf(f.view).sources[0], 1)
	if err != nil || slot != 0 {
		t.Fatal(slot, err)
	}
	if _, err := stableViewSlot(&compact, viewOf(f.view).sources[0], 0); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("missing identity admitted", err)
	}
	// Reject the first released classifier reason rather than a later group
	// restriction. These are admitted AST shapes, not invented parser nodes.
	restricted := *f.view
	definition := *viewOf(f.view)
	syntax := *definition.syntax
	syntax.Distinct = &parser.DistinctClause{}
	syntax.GroupBy = &parser.GroupClause{}
	definition.syntax = &syntax
	restricted.View = &definition
	_, _, _, reason := viewWriteShape(&restricted)
	if reason != "Views containing DISTINCT are not automatically updatable." {
		t.Fatal(reason)
	}
	syntax.Distinct = nil
	_, _, _, reason = viewWriteShape(&restricted)
	if reason != "Views containing GROUP BY are not automatically updatable." {
		t.Fatal(reason)
	}
	// Native dump replay adds explicit source aliases to preserve the frozen
	// layout. Those wrappers do not turn a single base source into a derived
	// query, and must not make a restored definition read-only.
	syntax.GroupBy = nil
	table := syntax.From
	syntax.From = &parser.AliasReference{Input: &parser.AliasReference{Input: table, Name: parser.Identifier{Text: "inner_source"}}, Name: parser.Identifier{Text: "restored_source"}}
	if _, _, _, reason = viewWriteShape(&restricted); reason != "" {
		t.Fatal("aliased ordinary source rejected", reason)
	}
	syntax.From = &parser.AliasReference{Input: &parser.DerivedQuery{}, Name: parser.Identifier{Text: "derived_source"}}
	if _, _, _, reason = viewWriteShape(&restricted); reason != "Views that do not select from a single table or view are not automatically updatable." {
		t.Fatal("aliased derived source admitted", reason)
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
}

func TestViewWriteDefaults(t *testing.T) {
	f := viewWriteMechanics(t)
	scope, op := f.scope(t)
	constant, _ := sqltype.Integer(f.integer, 42)
	f.base.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{19: {Expression: literalExpression{Info: expr.Info{Usage: f.integer.Usage()}, value: constant}}}
	b := binding{relation: f.view, ref: f.viewRef, targets: []int{0}, expressions: [][]expr.Expr{{nil}}}
	if err := f.e.bindViewWrite(&b, f.authorization(t, op), &parser.Insert{Rows: [][]parser.Expr{{parser.DefaultExpr{}}}}); err != nil {
		t.Fatal(err)
	}
	if len(b.defaults) != 3 || b.defaults[2] == nil || b.targets[0] != 1 {
		t.Fatal("hidden omission/default lost", b.targets, b.defaults)
	}
	// Conflict discovery consumes physical target authority, never the logical
	// view address, even when DO NOTHING has no explicit inference keys.
	indexAddress := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20002}
	f.e.catalog.state.SetDescriptor(indexAddress, &catalog.Relation{
		ObjectAddress: indexAddress,
		SQLIndex:      &catalog.IndexDefinition{Parent: f.base.Address(), Unique: true, Live: true, Ready: true, Valid: true},
	})
	conflict := &boundInsertConflict{}
	evaluation := EvalContext{Context: context.Background()}
	if err := conflict.selectArbiters(&evaluation, b.physicalBinding().relation, f.e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	if len(conflict.selected) != 1 || conflict.selected[0] != indexAddress || b.relation != f.view {
		t.Fatal("physical arbiter selection lost logical namespace", conflict.selected)
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
}

func TestViewWriteDemand(t *testing.T) {
	f := viewWriteMechanics(t)
	calls := 0
	unsafe := queryProbe(f.boolean.Usage(), func(*EvalContext) (sqltype.Value, error) {
		calls++
		return sqltype.Value{}, errors.New("hidden user work")
	})
	guard := literalExpression{Info: expr.Info{Usage: f.boolean.Usage()}, value: sqltype.Boolean(f.boolean, false)}
	expression := viewWritePredicate([]expr.Expr{guard}, unsafe)
	eval := EvalContext{Context: context.Background(), Scope: 1, Frame: 1}
	matched, err := updateMatches(&eval, expression)
	if err != nil || matched || calls != 0 {
		t.Fatal("hidden row demanded user work", matched, err, calls)
	}
	// Same-type analyzed casts preserve direct origin; COLLATE never does.
	ref := f.baseRef
	ref.Address.SubID = 11
	column := columnExpression{Info: expr.Info{Usage: f.integer.Usage()}, origin: ref, slot: 1}
	cast := coercionExpression{Info: column.Info, child: column}
	if _, ok := viewDirectColumn(cast); !ok {
		t.Fatal("no-op cast lost origin")
	}
	if _, ok := viewDirectColumn(collationExpression{Info: column.Info, child: column}); ok {
		t.Fatal("COLLATE gained write authority")
	}
	// Prepared/direct parameters are execution inputs, not stored-default
	// literals. Rewriting must retain their number/type/dependence unchanged.
	parameter := parameterExpression{
		Info:   expr.Info{Usage: f.integer.Usage(), Property: expr.Properties{ParameterDependent: true}},
		number: 1,
	}
	rewritten, changed, err := rewriteViewExpression(f.e.CatalogRead(), parameter, nil, func(node expr.Expr) (expr.Expr, bool, error) {
		return node, false, nil
	})
	if err != nil || changed {
		t.Fatal("unchanged parameter rewrite", changed, err)
	}
	retained, ok := rewritten.(parameterExpression)
	if !ok || retained.number != 1 || retained.ResultUsage() != parameter.ResultUsage() || retained.Properties() != parameter.Properties() {
		t.Fatal("parameter identity/type changed", rewritten)
	}
	value, err := sqltype.Integer(f.integer, 42)
	if err != nil {
		t.Fatal(err)
	}
	eval.Parameters = []sqltype.Value{value}
	got, err := rewritten.Eval(&eval)
	if err != nil {
		t.Fatal(err)
	}
	if number, err := got.Integer(); err != nil || number != 42 {
		t.Fatal("rewritten parameter lost imported argument", number, err)
	}
	t.Run("frozen_live_prefix", func(t *testing.T) {
		f := viewWriteMechanics(t)
		f.e.visibility.commitSeq = 2
		store := f.e.storage.Relations[f.base.Address()]
		store.Ends = &storage.DeletionRoot{Rows: map[ids.RowID]storage.RowEnd{1: {Version: 1, End: 2}}}
		scope, op := f.scope(t)
		leaves := []queryLeaf{{ref: f.baseRef, relation: f.base, storage: store, frozenAttributes: []int32{11, 7}}}
		f.e.mu.Lock()
		source, err := f.e.captureQuerySourcesLocked(scope, leaves, false)
		f.e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if source.input != nil || source.count() != 2 {
			source.close()
			t.Fatal("raw capture installed a bridge or selected prematurely")
		}
		if err := selectQuerySources(op.Context(), source); err != nil {
			t.Fatal(err)
		}
		source = freezeViewWriteSources(scope, source, leaves)
		if source.input == nil || source.count() != 1 {
			source.close()
			t.Fatal("frozen bridge exposed a deleted version")
		}
		row := source.row(0)
		a, aErr := row[0].Integer()
		id, idErr := row[1].Integer()
		source.close()
		if aErr != nil || idErr != nil || a != 20 || id != 2 {
			t.Fatal("selected source lost frozen attribute identities", a, id, aErr, idErr)
		}
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		lockEmpty(t, f.e)
	})
}

func TestViewWriteQueuedRecheck(t *testing.T) {
	f := viewWriteMechanics(t)
	holder, _ := f.scope(t)
	waiter, _ := f.scope(t)
	old := f.e.storage.Relations[f.base.Address()].Versions[0]
	resource := lock.Resource{ObjectRef: f.baseRef, Row: old.Row}
	if request, err := f.e.locks.StartAcquire(holder.op.Context(), holder.ticket, resource, lock.ForNoKeyUpdate, lock.Policy{}); err != nil || request != nil {
		t.Fatal(request, err)
	}
	request, err := f.e.locks.StartAcquire(waiter.op.Context(), waiter.ticket, resource, lock.ForNoKeyUpdate, lock.Policy{})
	if err != nil || request == nil {
		t.Fatal("not actually queued", request, err)
	}
	lockState := f.e.locks.Inspect()
	pending := len(lockState.Resources[resource].Pending)
	if pending != 1 {
		t.Fatal("missing physical queue", pending)
	}
	row := append([]sqltype.Value(nil), old.Values...)
	row[1], _ = sqltype.Integer(f.integer, 0)
	if err := holder.Writes().replace(f.baseRef, []storage.RowVersion{old}, indexWriteBatch{values: [][]sqltype.Value{row}}); err != nil {
		t.Fatal(err)
	}
	if err := holder.Finish(nil); err != nil {
		t.Fatal(err)
	}
	if err := request.Wait(); err != nil {
		t.Fatal(err)
	}
	current, live, err := f.e.latestUpdateRow(waiter, f.baseRef, updateCandidate{RowVersion: old, slot: 0})
	if err != nil || !live || current.Version == old.Version {
		t.Fatal(current, live, err)
	}
	predicate := queryProbe(f.boolean.Usage(), func(c *EvalContext) (sqltype.Value, error) {
		n, _ := c.Row[1].Integer()
		return sqltype.Boolean(f.boolean, n > 5), nil
	})
	eval := EvalContext{Context: context.Background(), Scope: 1, Frame: 1, Row: current.Values}
	matched, err := updateMatches(&eval, viewWritePredicate([]expr.Expr{predicate}, nil))
	if err != nil || matched {
		t.Fatal("latest predecessor bypassed view qualification", matched, err)
	}
	if err := waiter.Finish(nil); err != nil {
		t.Fatal(err)
	}
	lockEmpty(t, f.e)
}

func TestViewWriteCancellation(t *testing.T) {
	f := viewWriteMechanics(t)
	holder, _ := f.scope(t)
	scope, _ := f.scope(t)
	originals := f.e.storage.Relations[f.base.Address()].Versions
	resource := lock.Resource{ObjectRef: f.baseRef, Row: originals[0].Row}
	if request, err := f.e.locks.StartAcquire(holder.op.Context(), holder.ticket, resource, lock.ForNoKeyUpdate, lock.Policy{}); err != nil || request != nil {
		t.Fatal(request, err)
	}
	row := append([]sqltype.Value(nil), originals[1].Values...)
	row[1], _ = sqltype.Integer(f.integer, 99)
	if err := scope.Writes().replace(f.baseRef, []storage.RowVersion{originals[1]}, indexWriteBatch{values: [][]sqltype.Value{row}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(scope.op.Context())
	request, err := f.e.locks.StartAcquire(ctx, scope.ticket, resource, lock.ForNoKeyUpdate, lock.Policy{})
	if err != nil || request == nil {
		t.Fatal("missing actual pending row", request, err)
	}
	cancel()
	if err := request.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := scope.Finish(context.Canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	current := f.e.storage.Relations[f.base.Address()].Versions
	if current[0].Version != originals[0].Version || current[1].Version != originals[1].Version || f.e.visibility.commitSeq != 1 || len(scope.stage.rows) != 0 || scope.viewWrite != nil {
		t.Fatal("canceled stage published or retained resources")
	}
	lockState := f.e.locks.Inspect()
	entry := lockState.Resources[resource]
	pending := len(entry.Pending)
	hold := entry.Holders[holder.ticket].Mode
	if pending != 0 || hold != lock.ForNoKeyUpdate {
		t.Fatal("cancel damaged holder or leaked queue", pending, hold)
	}
	if err := holder.Finish(nil); err != nil {
		t.Fatal(err)
	}
	lockEmpty(t, f.e)
}

func TestViewWriteReplacement(t *testing.T) {
	f := viewWriteMechanics(t)
	scope, _ := f.scope(t, true)
	// Real explicit transaction admission permits native replacement without
	// releasing or forging an implicit statement's lifetime reservation.
	if err := f.e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.e.mu.Lock()
	err := scope.validateLockedRelation(f.e)
	f.e.mu.Unlock()
	if !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("old write grant survived replacement", err)
	}
	if err := scope.Finish(err); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal(err)
	}
	lockEmpty(t, f.e)
}
