package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
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
	baseRef := e.catalog.Domain.Ref(e.catalog.State.Epoch(), base.Address())
	column := func(slot int) expr.Expr {
		ref := baseRef
		ref.Address.SubID = base.Attributes[slot]
		return plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage(), Property: expr.Properties{RowDependent: true}}, Origin: ref, Scope: 1, Frame: 1, Slot: slot}
	}
	view := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20001}, ObjectNamespace: ns, ObjectName: "logical", ObjectRevision: 1, Owner: owner, RelationKind: catalog.ViewRelation, Attributes: []int32{1, 2}, ColumnMeta: catalog.ColumnMetadata{HighWater: 2}, Columns: []catalog.Column{{Name: "x", Usage: integer.Usage(), Nullable: true}, {Name: "key", Usage: integer.Usage(), Nullable: true}}, Index: map[string]int{"x": 0, "key": 1}}
	syntax := &parser.Select{From: &parser.TableReference{}}
	project := &plan.ProjectNode{QuerySchema: plan.QuerySchema(view.Columns), Frame: 1, Syntax: syntax, Child: &plan.SourceNode{QuerySchema: plan.QuerySchema(base.Columns), Ref: baseRef, Occurrence: 0}, Expressions: []expr.Expr{column(1), column(0)}}
	view.View = stored.NewView(stored.ViewParts{Syntax: syntax, Query: &plan.Query{Root: project}, Sources: []stored.ViewLeaf{{Ref: baseRef, Attributes: []int32{7, 11, 19}, Reads: []int32{7, 11}}}})
	viewRef := e.catalog.Domain.Ref(e.catalog.State.Epoch(), view.Address())
	e.catalog.State.SetDescriptor(base.Address(), base)
	e.catalog.State.SetDescriptor(view.Address(), view)
	e.catalog.State.BindName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: base.Name()}, base.Address())
	e.catalog.State.BindName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: view.Name()}, view.Address())
	value := func(n int64) sqltype.Value {
		v, err := sqltype.Integer(integer, n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	e.mu.Lock()
	e.storage.InstallRelationLocked(base.Address(), &storage.Relation{Generation: 1, Versions: []storage.RowVersion{{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{value(1), value(10), value(42)}}, {Row: 2, Version: 2, Born: 1, Values: []sqltype.Value{value(2), value(20), value(42)}}}})
	e.mu.Unlock()
	e.visibility.SetCommitSeqLocked(1)
	e.storage.SetFrontierLocked(2, 2)
	return viewWriteFixture{e, base, view, baseRef, viewRef, integer, boolean}
}

func (f viewWriteFixture) authorization(t *testing.T, op *session.Operation) AuthorizationView {
	t.Helper()
	f.e.mu.Lock()
	defer f.e.mu.Unlock()
	v, err := f.e.statementAuthorizationLocked(op, &parser.Update{})
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func (f viewWriteFixture) scope(t *testing.T, explicit ...bool) (*StatementScope, *session.Operation) {
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
	w := &view.Write{BaseRelation: f.base, Ref: f.baseRef, Layers: []view.WriteLayer{{Relation: f.view, Ref: f.viewRef, Source: f.base, SourceRef: f.baseRef}}}
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
	b := analyze.Binding{Relation: f.view, Ref: f.viewRef, Targets: []int{0}, Update: &analyze.UpdateBinding{Assignments: []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: f.integer.Usage()}, Value: sqltype.Null(f.integer)}}}}
	if err := view.BindViewWrite(&f.e.catalog.Owner, auth.Catalog, &b, &parser.Update{}); err != nil {
		t.Fatal(err)
	}
	if b.Relation != f.view || b.Ref != f.viewRef || b.Targets[0] != 1 || view.PhysicalBinding(b).Ref != f.baseRef {
		t.Fatal("logical/physical authority conflated", b.Targets)
	}
	// Creation slots survive current dense compaction and column renames.
	compact := *f.base
	compact.Attributes = []int32{11, 19}
	compact.Columns = []catalog.Column{f.base.Columns[1], f.base.Columns[2]}
	compact.Columns[0].Name = "renamed"
	slot, err := view.StableViewSlot(&compact, stored.ViewOf(f.view).Sources()[0], 1)
	if err != nil || slot != 0 {
		t.Fatal(slot, err)
	}
	if _, err := view.StableViewSlot(&compact, stored.ViewOf(f.view).Sources()[0], 0); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("missing identity admitted", err)
	}
	// Reject the first released classifier reason rather than a later group
	// restriction. These are admitted AST shapes, not invented parser nodes.
	restricted := *f.view
	syntax := *stored.ViewOf(f.view).Syntax()
	syntax.Distinct = &parser.DistinctClause{}
	syntax.GroupBy = &parser.GroupClause{}
	restricted.View = stored.ViewOf(f.view).With(func(p *stored.ViewParts) { p.Syntax = &syntax })
	_, _, _, reason := stored.ViewWriteShape(&restricted)
	if reason != "Views containing DISTINCT are not automatically updatable." {
		t.Fatal(reason)
	}
	syntax.Distinct = nil
	_, _, _, reason = stored.ViewWriteShape(&restricted)
	if reason != "Views containing GROUP BY are not automatically updatable." {
		t.Fatal(reason)
	}
	// Native dump replay adds explicit source aliases to preserve the frozen
	// layout. Those wrappers do not turn a single base source into a derived
	// query, and must not make a restored definition read-only.
	syntax.GroupBy = nil
	table := syntax.From
	syntax.From = &parser.AliasReference{Input: &parser.AliasReference{Input: table, Name: parser.Identifier{Text: "inner_source"}}, Name: parser.Identifier{Text: "restored_source"}}
	if _, _, _, reason = stored.ViewWriteShape(&restricted); reason != "" {
		t.Fatal("aliased ordinary source rejected", reason)
	}
	syntax.From = &parser.AliasReference{Input: &parser.DerivedQuery{}, Name: parser.Identifier{Text: "derived_source"}}
	if _, _, _, reason = stored.ViewWriteShape(&restricted); reason != "Views that do not select from a single table or view are not automatically updatable." {
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
	f.base.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{19: {Expression: plan.LiteralExpr{Info: expr.Info{Usage: f.integer.Usage()}, Value: constant}}}
	b := analyze.Binding{Relation: f.view, Ref: f.viewRef, Targets: []int{0}, Expressions: [][]expr.Expr{{nil}}}
	if err := view.BindViewWrite(&f.e.catalog.Owner, f.authorization(t, op).Catalog, &b, &parser.Insert{Rows: [][]parser.Expr{{parser.DefaultExpr{}}}}); err != nil {
		t.Fatal(err)
	}
	if len(b.Insert.Defaults) != 3 || b.Insert.Defaults[2] == nil || b.Targets[0] != 1 {
		t.Fatal("hidden omission/default lost", b.Targets, b.Insert.Defaults)
	}
	// Conflict discovery consumes physical target authority, never the logical
	// view address, even when DO NOTHING has no explicit inference keys.
	indexAddress := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20002}
	f.e.catalog.State.SetDescriptor(indexAddress, &catalog.Relation{
		ObjectAddress: indexAddress,
		SQLIndex:      &catalog.IndexDefinition{Method: catalog.BtreeMethod, MethodOID: 403, Parent: f.base.Address(), Unique: true, Live: true, Ready: true, Valid: true},
	})
	conflict := dml.NewInsertConflictState(&analyze.InsertConflict{})
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	if err := conflict.SelectArbiters(&evaluation, view.PhysicalBinding(b).Relation, f.e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	if len(conflict.Selected) != 1 || conflict.Selected[0] != indexAddress || b.Relation != f.view {
		t.Fatal("physical arbiter selection lost logical namespace", conflict.Selected)
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
}

func TestViewWriteDemand(t *testing.T) {
	f := viewWriteMechanics(t)
	calls := 0
	unsafe := queryProbe(f.boolean.Usage(), func(*exec.Context) (sqltype.Value, error) {
		calls++
		return sqltype.Value{}, errors.New("hidden user work")
	})
	guard := plan.LiteralExpr{Info: expr.Info{Usage: f.boolean.Usage()}, Value: sqltype.Boolean(f.boolean, false)}
	expression := view.ViewWritePredicate([]expr.Expr{guard}, unsafe)
	eval := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	matched, err := exec.UpdateMatches(&eval, expression)
	if err != nil || matched || calls != 0 {
		t.Fatal("hidden row demanded user work", matched, err, calls)
	}
	// Same-type analyzed casts preserve direct origin; COLLATE never does.
	ref := f.baseRef
	ref.Address.SubID = 11
	column := plan.ColumnExpr{Info: expr.Info{Usage: f.integer.Usage()}, Origin: ref, Slot: 1}
	cast := plan.CoercionExpr{Info: column.Info, Child: column}
	if _, ok := stored.ViewDirectColumn(cast); !ok {
		t.Fatal("no-op cast lost origin")
	}
	if _, ok := stored.ViewDirectColumn(plan.CollationExpr{Info: column.Info, Child: column}); ok {
		t.Fatal("COLLATE gained write authority")
	}
	// Prepared/direct parameters are execution inputs, not stored-default
	// literals. Rewriting must retain their number/type/dependence unchanged.
	parameter := plan.ParameterExpr{
		Info:   expr.Info{Usage: f.integer.Usage(), Property: expr.Properties{ParameterDependent: true}},
		Number: 1,
	}
	rewritten, changed, err := stored.RewriteViewExpression(f.e.CatalogRead(), parameter, nil, func(node expr.Expr) (expr.Expr, bool, error) {
		return node, false, nil
	})
	if err != nil || changed {
		t.Fatal("unchanged parameter rewrite", changed, err)
	}
	retained, ok := rewritten.(plan.ParameterExpr)
	if !ok || retained.Number != 1 || retained.ResultUsage() != parameter.ResultUsage() || retained.Properties() != parameter.Properties() {
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
		f.e.visibility.SetCommitSeqLocked(2)
		store := f.e.storage.Relations[f.base.Address()]
		store.Ends = &storage.DeletionRoot{Rows: map[ids.RowID]storage.RowEnd{1: {Version: 1, End: 2}}}
		scope, op := f.scope(t)
		leaves := []exec.Leaf{{Ref: f.baseRef, Relation: f.base, Storage: store, FrozenAttributes: []int32{11, 7}}}
		f.e.mu.Lock()
		source, err := f.e.captureQuerySourcesLocked(scope, leaves, false)
		f.e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if source.Input != nil || source.Count() != 2 {
			source.Close()
			t.Fatal("raw capture installed a bridge or selected prematurely")
		}
		if err := exec.SelectQuerySources(op.Context(), source); err != nil {
			t.Fatal(err)
		}
		source = view.FreezeWriteSources(source, leaves, scope.authority, hostOf(scope))
		if source.Input == nil || source.Count() != 1 {
			source.Close()
			t.Fatal("frozen bridge exposed a deleted version")
		}
		row := source.Row(0)
		a, aErr := row[0].Integer()
		id, idErr := row[1].Integer()
		source.Close()
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
	if err := dml.ReplaceRows(holder.writeHost(), f.baseRef, []storage.RowVersion{old}, index.WriteBatch{Values: [][]sqltype.Value{row}}, dml.ReplaceMerge); err != nil {
		t.Fatal(err)
	}
	if err := holder.Finish(nil); err != nil {
		t.Fatal(err)
	}
	if err := request.Wait(); err != nil {
		t.Fatal(err)
	}
	latest, err := waiter.writeHost().LatestRow(dml.UpdateRowRequest(f.baseRef, dml.UpdateCandidate{RowVersion: old, Slot: 0}))
	current, live := latest.Version, latest.Live
	if err != nil || !live || current.Version == old.Version {
		t.Fatal(current, live, err)
	}
	predicate := queryProbe(f.boolean.Usage(), func(c *exec.Context) (sqltype.Value, error) {
		n, _ := c.Row[1].Integer()
		return sqltype.Boolean(f.boolean, n > 5), nil
	})
	eval := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1, Row: current.Values}}
	matched, err := exec.UpdateMatches(&eval, view.ViewWritePredicate([]expr.Expr{predicate}, nil))
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
	if err := dml.ReplaceRows(scope.writeHost(), f.baseRef, []storage.RowVersion{originals[1]}, index.WriteBatch{Values: [][]sqltype.Value{row}}, dml.ReplaceMerge); err != nil {
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
	if current[0].Version != originals[0].Version || current[1].Version != originals[1].Version || f.e.visibility.CommitSeq() != 1 || len(scope.stage.rows) != 0 || scope.ViewWrite != nil {
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
