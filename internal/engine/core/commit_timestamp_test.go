package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func commitTimeUnitRef(e *core.Engine, id ids.TxID) ids.TxRef {
	return e.Catalog.Domain.Tx(ids.SessionToken{}, id, e.Catalog.State.Epoch(), nil, 0)
}

func TestCommitTimeAssignment(t *testing.T) {
	e := engine.NewWithSettings(nil, "", true)
	a, b := commitTimeUnitRef(e, 1), commitTimeUnitRef(e, 2)
	if e.Visibility.LastAssignedXID() != 2 || len(e.Visibility.CommitTimestamps) != 0 {
		t.Fatal("startup assigned a transaction")
	}
	if _, err := e.VisibilitySnapshotLocked(a, false); err != nil {
		t.Fatal(err)
	}
	if err := e.VisibilityWriteLocked(a); err != nil {
		t.Fatal(err)
	}
	first := e.Visibility.Entries[a]
	if first.XID != 1 || first.AssignedXID != 0 || len(e.Visibility.CommitTimestamps) != 0 {
		t.Fatal("potential writer admission assigned a public XID", first)
	}
	if err := e.VisibilityWriteLocked(a); err != nil || e.Visibility.Entries[a] != first {
		t.Fatal("repeated ordering admission changed identity", err)
	}
	e.Visibility.RecordCommitTimestampLocked(a)
	if e.Visibility.LastCommittedXID() != 0 {
		t.Fatal("ordering-only completion fabricated a commit")
	}
	// Public exhaustion cannot prevent independent visibility ordering.
	e.Visibility.SetLastAssignedXIDLocked(^uint64(0))
	if err := e.VisibilityWriteLocked(b); err != nil || e.Visibility.Entries[b].XID != 2 || e.Visibility.Entries[b].AssignedXID != 0 {
		t.Fatal("visibility ordering depends on public assignment", err)
	}
	e.Visibility.SetLastAssignedXIDLocked(2)
	if err := e.AssignTransactionXIDLocked(a); err != nil {
		t.Fatal(err)
	}
	if e.Visibility.Entries[a].AssignedXID != 3 || e.Visibility.Entries[a].XID != first.XID || e.Visibility.CommitTimestamps[3].Present {
		t.Fatal("actual assignment changed ordering or published history")
	}
	e.Visibility.DropSnapshotLocked(a)
	if e.Visibility.Entries[a].AssignedXID != 3 {
		t.Fatal("snapshot drop consumed actual assignment")
	}
	if err := e.AssignTransactionXIDLocked(b); err != nil {
		t.Fatal(err)
	}
	counter, slots, ordering := e.Visibility.LastAssignedXID(), len(e.Visibility.CommitTimestamps), e.Visibility.VisibilityXID()
	e.Visibility.SetLastAssignedXIDLocked(^uint64(0))
	c := commitTimeUnitRef(e, 3)
	if err := e.AssignTransactionXIDLocked(c); err == nil || e.Visibility.Entries[c] != (visibility.Entry{}) || len(e.Visibility.CommitTimestamps) != slots || e.Visibility.VisibilityXID() != ordering {
		t.Fatal("exhaustion partially assigned identity", err)
	}
	e.Visibility.SetLastAssignedXIDLocked(counter)
	if err := e.AssignTransactionXIDLocked(c); err != nil {
		t.Fatal(err)
	}
	if err := e.VisibilityWriteLocked(c); err != nil || e.Visibility.Entries[c].FullXID != counter+1 || uint64(e.Visibility.Entries[c].AssignedXID) != counter+1 || e.Visibility.Entries[c].XID != ordering+1 {
		t.Fatal("public and visibility authorities conflated", err)
	}
	d := commitTimeUnitRef(e, 4)
	if err := e.AssignTransactionXIDLocked(d); err != nil {
		t.Fatal(err)
	}
	e.Visibility.DropSnapshotLocked(d)
	if e.Visibility.Entries[d].XID != 0 || e.Visibility.Entries[d].AssignedXID == 0 {
		t.Fatal("assigned-only prepared ownership was discarded")
	}
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 16384}
	if core.PublicationNeedsXID(map[ids.ObjectAddress][]storage.RowVersion{address: nil}, map[ids.ObjectAddress]*visibility.PrivateDeletes{address: {}}, session.NewJournal(), nil) {
		t.Fatal("empty reserved publication containers fabricated assignment")
	}
	if !core.PublicationNeedsXID(nil, nil, nil, &session.PreparedPublication{Preparing: true}) {
		t.Fatal("empty PREPARE lost explicit assignment")
	}
	disabled := engine.New()
	ref := commitTimeUnitRef(disabled, 1)
	if err := disabled.AssignTransactionXIDLocked(ref); err != nil || len(disabled.Visibility.CommitTimestamps) != 0 {
		t.Fatal("disabled history allocation", err)
	}
	// Enrollment can follow assignment (for example a later captured snapshot).
	publicXID := disabled.Visibility.Entries[ref].AssignedXID
	core.EngineEnrollSerializableLocked(disabled, ref, session.Serializable)
	node := disabled.Visibility.Serializable.Nodes[ref]
	if node.AssignedXID != publicXID {
		t.Fatal("late SSI enrollment lost the assigned public identity")
	}
	disabled.Visibility.RecordCommitTimestampLocked(ref)
	if disabled.Visibility.Entries[ref].AssignedXID != 0 || node.AssignedXID != publicXID {
		t.Fatal("completion consumed the retained SSI diagnostic identity")
	}
}

func TestCommitTimePublication(t *testing.T) {
	for _, route := range []string{"rows", "empty", "catalog"} {
		for _, mode := range []string{"success", "cancel_before", "cancel_after"} {
			t.Run(route+"_"+mode, func(t *testing.T) {
				e := engine.NewWithSettings(nil, "", true)
				sess := sessionStart(t, e, nil)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				op, err := sess.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				tx := sess.Transactions
				tx.Mu.Lock()
				err = core.EngineBeginLocked(e, op, tx, session.TxImplicit, time.Now())
				tx.Mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				ref := tx.Ref
				if err := e.AssignTransactionXIDLocked(ref); err != nil {
					t.Fatal(err)
				}
				xid := e.Visibility.Entries[ref].AssignedXID
				old := e.Catalog.State
				var prepared *core.PreparedCatalogPublication
				var writes map[ids.ObjectAddress][]storage.RowVersion
				var rowStore *storage.Relation
				if route != "empty" {
					stage := e.BeginCatalogStage(e.CatalogRead())
					if route == "catalog" {
						stage.InitialTx = ref
					}
					d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: catalogUnitOID(t, e)}, Owner: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "commit_time_unit", ObjectRevision: 1, Columns: []catalog.Column{{Name: "id", Usage: enginetest.Usage(e, sqltype.Int4)}}, Index: map[string]int{"id": 0}, Attributes: []int32{1}, ColumnMeta: catalog.ColumnMetadata{HighWater: 1}}
					d.TableMethod = 2
					if err := stage.Create(d); err != nil {
						t.Fatal(err)
					}
					prepared, err = e.ValidateCatalogStage(ctx, stage)
					if err != nil {
						t.Fatal(err)
					}
					if route == "rows" {
						if _, err := enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: old.Epoch()}); err != nil {
							t.Fatal(err)
						}
						old = e.Catalog.State
						value, err := sqltype.Integer(d.Columns[0].Usage.Type, 7)
						if err != nil {
							t.Fatal(err)
						}
						versions, err := e.Storage.AllocateRowVersionsLocked([][]sqltype.Value{{value}}, e.Catalog.State)
						if err != nil {
							t.Fatal(err)
						}
						writes = map[ids.ObjectAddress][]storage.RowVersion{d.Address(): versions}
						rowStore = e.Storage.Relations[d.Address()]
					}
				}
				barrier := &transactionBarrierContext{Context: ctx, entered: make(chan struct{}), release: make(chan struct{})}
				if route == "catalog" {
					// Two existing final p.ctx checks surround authority validation.
					core.SetPreparedCatalogPublicationCtx(prepared, barrier)
					barrier.checks.Store(1)
				} else {
					// With no indexes, both row paths have one final operation preflight.
					op.Ctx = barrier
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
						_, failure := e.PublishCatalogOperation(op, nil, prepared, core.PublicationToken{Epoch: old.Epoch()})
						done <- failure
					} else {
						done <- core.EnginePublishRowsPrepared(e, op, ref, writes, nil, nil, nil, nil, nil)
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
					e.Mu.Lock()
					snapshot := [3]bool{e.Catalog.State != old, rowStore != nil && len(rowStore.Versions) != 0, e.Visibility.LastCommittedXID() == xid && e.Visibility.CommitTimestamps[uint64(xid)].Present}
					e.Mu.Unlock()
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
					if !errors.Is(err, context.Canceled) || e.Visibility.LastCommittedXID() != 0 || e.Visibility.CommitTimestamps[uint64(xid)].Present || e.Catalog.State != old {
						t.Fatal("failed publication leaked a commit", err)
					}
				} else {
					if err != nil || e.Visibility.LastCommittedXID() != xid || !e.Visibility.CommitTimestamps[uint64(xid)].Present || e.Visibility.LastCommitTimestamp().Stamp.Before(before) || e.Visibility.LastCommitTimestamp().Origin != 0 {
						t.Fatal("successful publication missing commit", err)
					}
					stamp := e.Visibility.LastCommitTimestamp()
					// A successful catalog publisher has already consumed completion.
					e.Visibility.RecordCommitTimestampLocked(ref)
					if e.Visibility.LastCommitTimestamp() != stamp || e.Visibility.Entries[ref].AssignedXID != 0 {
						t.Fatal("double completion")
					}
				}
			})
		}
	}
}

func TestCommitTimeReplacement(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		e := engine.NewWithSettings(nil, "", true)
		ref := commitTimeUnitRef(e, 1)
		if err := e.AssignTransactionXIDLocked(ref); err != nil {
			t.Fatal(err)
		}
		xid := e.Visibility.Entries[ref].AssignedXID
		e.Visibility.RecordCommitTimestampLocked(ref)
		source := e
		if foreign {
			source = engine.New()
		}
		view := source.CatalogRead()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := e.ReplaceCatalog(ctx, source, view); !errors.Is(err, context.Canceled) || e.Visibility.LastCommittedXID() != xid {
			t.Fatal("failed replacement changed history", err)
		}
		if _, err := e.ReplaceCatalog(context.Background(), source, view); err != nil {
			t.Fatal(err)
		}
		if !e.Visibility.TracksCommitTimestamps() || e.Visibility.LastCommittedXID() != 0 || len(e.Visibility.CommitTimestamps) != 0 || e.Visibility.CommitTimestampBase() != uint64(xid)+1 || e.Visibility.LastAssignedXID() != uint64(xid) {
			t.Fatal("replacement imported or reused history")
		}
		if err := e.AssignTransactionXIDLocked(commitTimeUnitRef(e, 2)); err != nil || e.Visibility.LastAssignedXID() != uint64(xid)+1 {
			t.Fatal("replacement identity reuse", err)
		}
	}
}

func TestCommitTimeExpression(t *testing.T) {
	ctx := context.Background()
	e := engine.New()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	for _, name := range []string{"pg_xact_commit_timestamp", "pg_xact_commit_timestamp_origin", "pg_last_committed_xact"} {
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}}
		if name != "pg_last_committed_xact" {
			raw.Args = []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}}
		}
		bound, err := b.BindFunctionProvider(raw)
		if err != nil {
			t.Fatal(err)
		}
		n := bound
		if err := n.(expr.StoredProvider).ValidateStoredProvider(); err != nil {
			t.Fatal(err)
		}
		if n.Properties().Volatility != expr.Volatile || exec.PlannedVolatility(n) != expr.Volatile || plan.GenerationImmutable(n) || exec.ConditionalConstantInputs(n) || !analyze.SameGroupExpression(n, n) {
			t.Fatal("volatile expression lifecycle")
		}
		count := 0
		plan.ExpressionChildren(n, func(expr.Expr) { count++ })
		if count != len(raw.Args) {
			t.Fatal("child traversal", count)
		}
		if name != "pg_last_committed_xact" {
			v, err := n.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
			if err != nil || !v.IsNull() || v.Type() != n.ResultUsage().Type {
				t.Fatal("strict whole NULL", err)
			}
		}
		recipe, err := (stored.ScalarRenderer{}).ScalarFunctionSQL(ctx, e.CatalogRead(), n)
		if err != nil || !strings.Contains(recipe, "pg_catalog."+name+"(") {
			t.Fatal(recipe, err)
		}
		persisted, err := stored.RemapExpression(n, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
		if err != nil || !analyze.SameGroupExpression(n, persisted) || persisted.(expr.StoredProvider).ValidateStoredProvider() != nil {
			t.Fatal("symbolic persistence lost the inquiry signature or argument", err)
		}
		if len(n.(expr.Composite).Children()) != 0 {
			column := plan.ColumnExpr{
				Info:   b.Info(b.Type(sqltype.Xid).Usage(), parser.Position{}, expr.Properties{RowDependent: true}),
				Origin: ids.ObjectRef{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 16384, SubID: 1}},
			}
			info := expr.InfoOf(n)
			info.Property = expr.MergeProperties(expr.Properties{Volatility: expr.Volatile}, column.Properties())
			withColumn, err := n.(expr.Composite).Rebuild(info, []expr.Expr{column})
			if err != nil {
				t.Fatal(err)
			}
			persisted, err := stored.RemapExpression(withColumn, func(ref ids.ObjectRef) ids.ObjectRef {
				ref.Address.SubID++
				return ref
			}, map[int32]int{2: 3})
			if err != nil {
				t.Fatal("symbolic inquiry child remap", err)
			}
			next := persisted
			mappedColumn := next.(expr.Composite).Children()[0].(plan.ColumnExpr)
			if next.(expr.StoredProvider).ValidateStoredProvider() != nil || expr.InfoOf(next).CallRoutineReference() != expr.InfoOf(n).CallRoutineReference() || next.Properties() != withColumn.Properties() || next.ResultUsage() != n.ResultUsage() || mappedColumn.Origin.Address.SubID != 2 || mappedColumn.Slot != 3 {
				t.Fatal("persistence failed to remap the inquiry's child")
			}
			originalColumn := withColumn.(expr.Composite).Children()[0].(plan.ColumnExpr)
			if originalColumn.Origin.Address.SubID != 1 || originalColumn.Slot != 0 {
				t.Fatal("persistence mutated the original inquiry child")
			}
		}
		fresh := engine.New()
		fb := newTestExpressionBinder(t, ctx, fresh, sqltype.InputEnvironment{}, nil)
		mapped, _, err := stored.RetagTypes(fresh.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor {
			if d.IsComposite() {
				return d
			}
			return fb.Type(d.ID())
		})
		if err != nil || mapped.(expr.StoredProvider).ValidateStoredProvider() != nil {
			t.Fatal("retagged provider", err)
		}
	}
}
