package engine

import (
	"context"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestSnapshotBinding(t *testing.T) {
	e := New()
	ctx := context.Background()
	for _, tc := range []struct {
		name       string
		oid        uint32
		result     sqltype.ID
		argc       int
		volatility expr.Volatility
	}{
		{"pg_current_snapshot", 5061, sqltype.PgSnapshot, 0, expr.Stable},
		{"pg_snapshot_xmin", 5062, sqltype.Xid8, 1, expr.Immutable},
		{"pg_snapshot_xmax", 5063, sqltype.Xid8, 1, expr.Immutable},
		{"pg_snapshot_xip", 5064, sqltype.Xid8, 1, expr.Immutable},
		{"pg_visible_in_snapshot", 5065, sqltype.Bool, 2, expr.Immutable},
	} {
		b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		b.Clause.AggregateClause = "SELECT"
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: tc.name}}}}
		for range tc.argc {
			raw.Args = append(raw.Args, parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}})
		}
		node, err := b.BindFunctionProvider(raw)
		if err != nil {
			t.Fatal(tc.name, err)
		}
		if node.ResultUsage().Type.ID() != tc.result || exec.PlannedVolatility(node) != tc.volatility {
			t.Fatal(tc.name, node.ResultUsage(), exec.PlannedVolatility(node))
		}
		ref := expr.InfoOf(node).CallRoutineReference()
		if ref == nil || ref.RoutineOID() != tc.oid {
			t.Fatal(tc.name, ref)
		}
		if err := node.(expr.StoredProvider).ValidateStoredProvider(); err != nil {
			t.Fatal(err)
		}
		_, set := expr.SourceTraits(node)
		if set != (tc.name == "pg_snapshot_xip") {
			t.Fatal("cardinality", tc.name)
		}
		if b.Clause.QueryHasTargetSet != set || node.Properties().StageDependent != set {
			t.Fatal("target set scheduling", tc.name, b.Clause.QueryHasTargetSet, node.Properties())
		}
		recipe, err := (stored.ScalarRenderer{}).ScalarFunctionSQL(ctx, e.CatalogRead(), node)
		if err != nil || recipe == "" {
			t.Fatal(recipe, err)
		}
	}
}

func TestSnapshotStatementRetention(t *testing.T) {
	for _, isolation := range []session.Isolation{session.ReadCommitted, session.RepeatableRead, session.Serializable} {
		t.Run(isolationName(isolation), func(t *testing.T) {
			e := New()
			stage, relation := queryCreateStage(t, e, "snapshot_rows", 1)
			queryCreatePublish(t, e, stage)
			e.Mu.Lock()
			e.Visibility.SetCommitSeqLocked(10)
			rows := e.Storage.Relations[relation.Address()]
			rows.Versions = []storage.RowVersion{{Row: 1, Version: 1, Born: 10}}
			e.Mu.Unlock()
			_, peer := blockingUnitOwner(t, e)
			_, bumper := blockingUnitOwner(t, e)
			pgLocksUnitAssign(t, e, peer)
			pgLocksUnitAssign(t, e, bumper)
			e.Mu.Lock()
			e.Visibility.EndTransactionIdentityLocked(bumper.Ref(), true)
			e.Mu.Unlock()
			observer, _ := blockingUnitOwner(t, e)
			observer.Transactions.Mu.Lock()
			observer.Transactions.Isolation = isolation
			observer.Transactions.Mu.Unlock()
			scope, op, cancel := concurrentUnitSnapshotScope(t, e, observer)
			if err := scope.snapshotAnalysisPhase(&parser.Select{}); err != nil {
				t.Fatal(err)
			}
			id := uint64(peer.AssignedFullXID())
			analysis := observer.Transactions.CurrentSnapshot
			if isolation.Stable() {
				if analysis == nil || analysis.Visible(id) || scope.snapshotFrontier != 10 || observer.Transactions.Frontier != 10 || len(observer.Transactions.Roots[relation.Address()].Versions) != 1 {
					t.Fatal("analysis did not retain one public/tuple pair")
				}
			} else if analysis != nil || scope.currentSnapshot != nil || observer.Transactions.SnapshotSet {
				t.Fatal("RC analysis retained execution authority")
			}
			e.Mu.Lock()
			rows.Versions = append(rows.Versions, storage.RowVersion{Row: 2, Version: 2, Born: 11})
			e.Visibility.AdvanceCommitSeqLocked()
			e.Visibility.EndTransactionIdentityLocked(peer.Ref(), true)
			e.Mu.Unlock()
			if err := scope.snapshotDataPhase(); err != nil {
				t.Fatal(err)
			}
			saved := scope.currentSnapshot
			wantRows := []ids.RowID{1, 2}
			wantFrontier := ids.CommitSeq(11)
			if isolation.Stable() {
				wantRows, wantFrontier = wantRows[:1], 10
				if saved != analysis {
					t.Fatal("execution replaced stable analysis snapshot")
				}
			}
			if saved == nil || saved.Visible(id) == isolation.Stable() || scope.snapshotFrontier != wantFrontier {
				t.Fatal("execution public/tuple frontier mismatch", saved, scope.snapshotFrontier)
			}
			ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), relation.Address())
			snapshotUnitRows(t, scope, ref, wantRows)
			if err := scope.snapshotDataPhase(); err != nil || scope.currentSnapshot != saved || scope.snapshotFrontier != wantFrontier {
				t.Fatal("command snapshot refreshed", err)
			}
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
			op.Done()
			cancel()
			next, nextOp, nextCancel := concurrentUnitSnapshotScope(t, e, observer)
			if err := next.snapshotDataPhase(); err != nil {
				t.Fatal(err)
			}
			if isolation.Stable() {
				if next.currentSnapshot != saved || next.currentSnapshot.Visible(id) {
					t.Fatal("stable snapshot lost")
				}
			} else if !next.currentSnapshot.Visible(id) || next.currentSnapshot == saved {
				t.Fatal("read committed did not refresh")
			}
			if err := next.Finish(nil); err != nil {
				t.Fatal(err)
			}
			nextOp.Done()
			nextCancel()
		})
	}
}
func isolationName(level session.Isolation) string {
	switch level {
	case session.ReadCommitted:
		return "read-committed"
	case session.RepeatableRead:
		return "repeatable-read"
	default:
		return "serializable"
	}
}
