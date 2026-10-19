package engine

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func commitTimeUnitRef(e *Engine, id ids.TxID) ids.TxRef {
	return e.catalog.Domain.Tx(ids.SessionToken{}, id, e.catalog.state.Epoch(), nil, 0)
}

func TestCommitTimeAssignment(t *testing.T) {
	e := NewWithSettings(nil, "", true)
	a, b := commitTimeUnitRef(e, 1), commitTimeUnitRef(e, 2)
	if e.visibility.assignedXID != 2 || len(e.visibility.commitTimestamps) != 0 {
		t.Fatal("startup assigned a transaction")
	}
	if _, err := e.visibilitySnapshotLocked(a, false); err != nil {
		t.Fatal(err)
	}
	if err := e.visibilityWriteLocked(a); err != nil {
		t.Fatal(err)
	}
	first := e.visibility.entries[a]
	if first.xid != 1 || first.assignedXID != 0 || len(e.visibility.commitTimestamps) != 0 {
		t.Fatal("potential writer admission assigned a public XID", first)
	}
	if err := e.visibilityWriteLocked(a); err != nil || e.visibility.entries[a] != first {
		t.Fatal("repeated ordering admission changed identity", err)
	}
	e.visibility.recordCommitTimestampLocked(a)
	if e.visibility.lastCommittedXID != 0 {
		t.Fatal("ordering-only completion fabricated a commit")
	}
	// Public exhaustion cannot prevent independent visibility ordering.
	e.visibility.assignedXID = ^uint64(0)
	if err := e.visibilityWriteLocked(b); err != nil || e.visibility.entries[b].xid != 2 || e.visibility.entries[b].assignedXID != 0 {
		t.Fatal("visibility ordering depends on public assignment", err)
	}
	e.visibility.assignedXID = 2
	if err := e.assignTransactionXIDLocked(a); err != nil {
		t.Fatal(err)
	}
	if e.visibility.entries[a].assignedXID != 3 || e.visibility.entries[a].xid != first.xid || e.visibility.commitTimestamps[3].present {
		t.Fatal("actual assignment changed ordering or published history")
	}
	e.visibility.dropSnapshotLocked(a)
	if e.visibility.entries[a].assignedXID != 3 {
		t.Fatal("snapshot drop consumed actual assignment")
	}
	if err := e.assignTransactionXIDLocked(b); err != nil {
		t.Fatal(err)
	}
	counter, slots, ordering := e.visibility.assignedXID, len(e.visibility.commitTimestamps), e.visibility.visibilityXID
	e.visibility.assignedXID = ^uint64(0)
	c := commitTimeUnitRef(e, 3)
	if err := e.assignTransactionXIDLocked(c); err == nil || e.visibility.entries[c] != (transactionVisibility{}) || len(e.visibility.commitTimestamps) != slots || e.visibility.visibilityXID != ordering {
		t.Fatal("exhaustion partially assigned identity", err)
	}
	e.visibility.assignedXID = counter
	if err := e.assignTransactionXIDLocked(c); err != nil {
		t.Fatal(err)
	}
	if err := e.visibilityWriteLocked(c); err != nil || e.visibility.entries[c].fullXID != counter+1 || uint64(e.visibility.entries[c].assignedXID) != counter+1 || e.visibility.entries[c].xid != ordering+1 {
		t.Fatal("public and visibility authorities conflated", err)
	}
	d := commitTimeUnitRef(e, 4)
	if err := e.assignTransactionXIDLocked(d); err != nil {
		t.Fatal(err)
	}
	e.visibility.dropSnapshotLocked(d)
	if e.visibility.entries[d].xid != 0 || e.visibility.entries[d].assignedXID == 0 {
		t.Fatal("assigned-only prepared ownership was discarded")
	}
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 16384}
	if publicationNeedsXID(map[ids.ObjectAddress][]storage.RowVersion{address: nil}, map[ids.ObjectAddress]*privateDeletionRoot{address: {}}, newCatalogChanges(), nil) {
		t.Fatal("empty reserved publication containers fabricated assignment")
	}
	if !publicationNeedsXID(nil, nil, nil, &preparedPublication{preparing: true}) {
		t.Fatal("empty PREPARE lost explicit assignment")
	}
	disabled := New()
	ref := commitTimeUnitRef(disabled, 1)
	if err := disabled.assignTransactionXIDLocked(ref); err != nil || len(disabled.visibility.commitTimestamps) != 0 {
		t.Fatal("disabled history allocation", err)
	}
	// Enrollment can follow assignment (for example a later captured snapshot).
	publicXID := disabled.visibility.entries[ref].assignedXID
	disabled.enrollSerializableLocked(ref, Serializable)
	node := disabled.visibility.serializable.nodes[ref]
	if node.assignedXID != publicXID {
		t.Fatal("late SSI enrollment lost the assigned public identity")
	}
	disabled.visibility.recordCommitTimestampLocked(ref)
	if disabled.visibility.entries[ref].assignedXID != 0 || node.assignedXID != publicXID {
		t.Fatal("completion consumed the retained SSI diagnostic identity")
	}
}

func TestCommitTimePublication(t *testing.T) {
	for _, route := range []string{"rows", "empty", "catalog"} {
		for _, mode := range []string{"success", "cancel_before", "cancel_after"} {
			t.Run(route+"_"+mode, func(t *testing.T) {
				e := NewWithSettings(nil, "", true)
				session := sessionStart(t, e, nil)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				op, err := session.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				tx := session.transactions
				tx.mu.Lock()
				err = e.beginLocked(op, tx, TxImplicit, time.Now())
				tx.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				ref := tx.ref
				if err := e.assignTransactionXIDLocked(ref); err != nil {
					t.Fatal(err)
				}
				xid := e.visibility.entries[ref].assignedXID
				old := e.catalog.state
				var prepared *PreparedCatalogPublication
				var writes map[ids.ObjectAddress][]storage.RowVersion
				var rowStore *storage.Relation
				if route != "empty" {
					stage := e.BeginCatalogStage(e.CatalogRead())
					if route == "catalog" {
						stage.initialTx = ref
					}
					d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: catalogUnitOID(t, e)}, Owner: e.roles.published.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "commit_time_unit", ObjectRevision: 1, Columns: []catalog.Column{{Name: "id", Usage: testUsage(e, sqltype.Int4)}}, Index: map[string]int{"id": 0}, Attributes: []int32{1}, ColumnMeta: catalog.ColumnMetadata{HighWater: 1}}
					if err := stage.Create(d); err != nil {
						t.Fatal(err)
					}
					prepared, err = e.ValidateCatalogStage(ctx, stage)
					if err != nil {
						t.Fatal(err)
					}
					if route == "rows" {
						if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: old.Epoch()}); err != nil {
							t.Fatal(err)
						}
						old = e.catalog.state
						value, err := sqltype.Integer(d.Columns[0].Usage.Type, 7)
						if err != nil {
							t.Fatal(err)
						}
						versions, err := e.storage.AllocateRowVersionsLocked([][]sqltype.Value{{value}}, e.catalog.state)
						if err != nil {
							t.Fatal(err)
						}
						writes = map[ids.ObjectAddress][]storage.RowVersion{d.Address(): versions}
						rowStore = e.storage.Relations[d.Address()]
					}
				}
				barrier := &transactionBarrierContext{Context: ctx, entered: make(chan struct{}), release: make(chan struct{})}
				if route == "catalog" {
					// Two existing final p.ctx checks surround authority validation.
					prepared.ctx = barrier
					barrier.checks.Store(1)
				} else {
					// With no indexes, both row paths have one final operation preflight.
					op.ctx = barrier
					barrier.checks.Store(2)
				}
				if mode == "cancel_after" {
					barrier.late = cancel
				}
				done := make(chan error, 1)
				joined := false
				defer func() {
					cancel()
					select {
					case <-barrier.release:
					default:
						close(barrier.release)
					}
					if !joined {
						select {
						case <-done:
						case <-time.After(5 * time.Second):
							t.Error("publication worker did not stop")
						}
					}
				}()
				before := time.Now().UTC().Truncate(time.Microsecond)
				go func() {
					if route == "catalog" {
						_, failure := e.publishCatalogOperation(op, nil, prepared, PublicationToken{Epoch: old.Epoch()})
						done <- failure
					} else {
						done <- e.publishRowsPrepared(op, ref, writes, nil, nil, nil, nil, nil)
					}
				}()
				select {
				case <-barrier.entered:
				case failure := <-done:
					joined = true
					t.Fatal("publisher failed before final boundary", failure)
				case <-time.After(5 * time.Second):
					t.Fatal("publisher did not reach final boundary")
				}
				observed := make(chan [3]bool, 1)
				go func() {
					e.mu.Lock()
					snapshot := [3]bool{e.catalog.state != old, rowStore != nil && len(rowStore.Versions) != 0, e.visibility.lastCommittedXID == xid && e.visibility.commitTimestamps[uint64(xid)].present}
					e.mu.Unlock()
					observed <- snapshot
				}()
				if mode == "cancel_before" {
					cancel()
				}
				close(barrier.release)
				select {
				case err = <-done:
					joined = true
				case <-time.After(5 * time.Second):
					t.Fatal("publisher did not finish")
				}
				committed := mode != "cancel_before"
				select {
				case snapshot := <-observed:
					if snapshot != [3]bool{route == "catalog" && committed, route == "rows" && committed, committed} {
						t.Fatal("torn publication/history observation", snapshot)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("publication observer did not finish")
				}
				if !committed {
					if !errors.Is(err, context.Canceled) || e.visibility.lastCommittedXID != 0 || e.visibility.commitTimestamps[uint64(xid)].present || e.catalog.state != old {
						t.Fatal("failed publication leaked a commit", err)
					}
				} else {
					if err != nil || e.visibility.lastCommittedXID != xid || !e.visibility.commitTimestamps[uint64(xid)].present || e.visibility.lastCommitTimestamp.stamp.Before(before) || e.visibility.lastCommitTimestamp.origin != 0 {
						t.Fatal("successful publication missing commit", err)
					}
					stamp := e.visibility.lastCommitTimestamp
					// A successful catalog publisher has already consumed completion.
					e.visibility.recordCommitTimestampLocked(ref)
					if e.visibility.lastCommitTimestamp != stamp || e.visibility.entries[ref].assignedXID != 0 {
						t.Fatal("double completion")
					}
				}
			})
		}
	}
}

func TestCommitTimeReplacement(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		e := NewWithSettings(nil, "", true)
		ref := commitTimeUnitRef(e, 1)
		if err := e.assignTransactionXIDLocked(ref); err != nil {
			t.Fatal(err)
		}
		xid := e.visibility.entries[ref].assignedXID
		e.visibility.recordCommitTimestampLocked(ref)
		source := e
		if foreign {
			source = New()
		}
		view := source.CatalogRead()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := e.ReplaceCatalog(ctx, source, view); !errors.Is(err, context.Canceled) || e.visibility.lastCommittedXID != xid {
			t.Fatal("failed replacement changed history", err)
		}
		if _, err := e.ReplaceCatalog(context.Background(), source, view); err != nil {
			t.Fatal(err)
		}
		if !e.visibility.trackCommitTimestamp || e.visibility.lastCommittedXID != 0 || len(e.visibility.commitTimestamps) != 0 || e.visibility.commitTimestampBase != uint64(xid)+1 || e.visibility.assignedXID != uint64(xid) {
			t.Fatal("replacement imported or reused history")
		}
		if err := e.assignTransactionXIDLocked(commitTimeUnitRef(e, 2)); err != nil || e.visibility.assignedXID != uint64(xid)+1 {
			t.Fatal("replacement identity reuse", err)
		}
	}
}

func TestCommitTimeExpression(t *testing.T) {
	ctx := context.Background()
	e := New()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	for _, kind := range []commitTimestampKind{commitTimestampScalar, commitTimestampOrigin, commitTimestampLast} {
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: kind.name()}}}}
		if kind != commitTimestampLast {
			raw.Args = []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}}
		}
		bound, err := b.bindCommitTimestamp(raw)
		if err != nil {
			t.Fatal(err)
		}
		n := bound.(commitTimestampExpression)
		if err := n.validate(); err != nil {
			t.Fatal(err)
		}
		if n.Properties().Volatility != expr.Volatile || plannedVolatility(n) != expr.Volatile || generationImmutable(n) || conditionalConstantInputs(n) || !sameGroupExpression(n, n) {
			t.Fatal("volatile expression lifecycle")
		}
		count := 0
		boundExpressionChildren(n, func(expr.Expr) { count++ })
		if count != len(raw.Args) {
			t.Fatal("child traversal", count)
		}
		if kind != commitTimestampLast {
			v, err := n.Eval(&EvalContext{Context: ctx})
			if err != nil || !v.IsNull() || v.Type() != n.Usage.Type {
				t.Fatal("strict whole NULL", err)
			}
		}
		recipe, err := (scalarRenderer{}).scalarFunctionSQL(ctx, e.CatalogRead(), n)
		if err != nil || !strings.Contains(recipe, "pg_catalog."+kind.name()+"(") {
			t.Fatal(recipe, err)
		}
		persisted, err := remapBoundExpression(n, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
		if err != nil || !sameGroupExpression(n, persisted) || persisted.(commitTimestampExpression).validate() != nil {
			t.Fatal("symbolic persistence lost the inquiry signature or argument", err)
		}
		if n.arg != nil {
			column := columnExpression{
				Info:   b.info(b.typ(sqltype.Xid).Usage(), parser.Position{}, expr.Properties{RowDependent: true}),
				origin: ids.ObjectRef{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 16384, SubID: 1}},
			}
			withColumn := n
			withColumn.arg = column
			withColumn.Property = expr.MergeProperties(expr.Properties{Volatility: expr.Volatile}, column.Properties())
			persisted, err := remapBoundExpression(withColumn, func(ref ids.ObjectRef) ids.ObjectRef {
				ref.Address.SubID++
				return ref
			}, map[int32]int{2: 3})
			if err != nil {
				t.Fatal("symbolic inquiry child remap", err)
			}
			next := persisted.(commitTimestampExpression)
			mappedColumn := next.arg.(columnExpression)
			if next.kind != kind || next.validate() != nil || mappedColumn.origin.Address.SubID != 2 || mappedColumn.slot != 3 {
				t.Fatal("persistence failed to remap the inquiry's child")
			}
			originalColumn := withColumn.arg.(columnExpression)
			if originalColumn.origin.Address.SubID != 1 || originalColumn.slot != 0 {
				t.Fatal("persistence mutated the original inquiry child")
			}
		}
		fresh := New()
		fb := newTestExpressionBinder(t, ctx, fresh, sqltype.InputEnvironment{}, nil)
		mapped, _, err := retagBoundTypes(fresh.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor {
			if d.IsComposite() {
				return d
			}
			return fb.typ(d.ID())
		})
		if err != nil || mapped.(commitTimestampExpression).validate() != nil {
			t.Fatal("retagged provider", err)
		}
	}
}
