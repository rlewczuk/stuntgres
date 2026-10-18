package engine

import (
	"context"
	"stuntgres/internal/parser"
	"testing"
)

func refreshUnitScope(t *testing.T, e *Engine, ctx context.Context) *StatementScope {
	t.Helper()
	session := sessionStart(t, e, nil)
	op, err := session.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.Finish(nil); op.Done() })
	stmt := &parser.RefreshMaterializedView{}
	if err := scope.gate(stmt); err != nil {
		t.Fatal(err)
	}
	if err := scope.snapshotPhase(stmt); err != nil {
		t.Fatal(err)
	}
	return scope
}

func TestMaterializedRefreshLocks(t *testing.T) {
	for _, tc := range []struct {
		name            string
		target          bool
		held            relationLockMode
		noData, blocked bool
	}{
		{"reader_target", true, accessShare, false, true},
		{"refresh_target", true, accessExclusive, false, true},
		{"source_exclusive", false, accessExclusive, false, true},
		{"source_insert", false, rowExclusive, false, false},
		{"no_data_source_exclusive", false, accessExclusive, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			d := refreshUnitRelation(t, e)
			stage, source := queryCreateStage(t, e, "refresh_unit_source", 1)
			queryCreatePublish(t, e, stage)
			sourceRef := e.domain.Ref(e.state.epoch, source.address)
			definition := *d.view
			definition.sources = []viewLeaf{{ref: sourceRef, attributes: source.attributes, reads: source.attributes}}
			d.view = &definition
			holder := sessionStart(t, e, nil)
			tx := transactionBegin(t, holder)
			ref := sourceRef
			if tc.target {
				ref.Address = d.address
			}
			if err := e.locks.Acquire(context.Background(), holder.transactions.ticket, lockResource{ObjectRef: ref}, tc.held); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			scope := refreshUnitScope(t, e, ctx)
			stmt := &parser.RefreshMaterializedView{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: d.name}}}, NoData: tc.noData}
			done := make(chan error, 1)
			go func() { done <- scope.acquireMaterializedRefresh(stmt) }()
			joined := false
			defer func() {
				cancel()
				transactionEnd(t, holder, tx, false)
				if !joined {
					lockAwait(t, done, context.Canceled)
				}
			}()
			if tc.blocked {
				lockPending(t, e, 1)
				cancel()
				lockAwait(t, done, context.Canceled)
				joined = true
				if err := scope.Finish(context.Canceled); err == nil {
					t.Fatal("canceled refresh scope succeeded")
				}
			} else {
				lockAwait(t, done, nil)
				joined = true
				if tc.noData && len(scope.materializedSources) != 0 {
					t.Fatal("NO DATA traversed sources")
				}
				if !tc.noData && len(scope.materializedSources) != 2 {
					t.Fatal("DATA did not retain source fences")
				}
				if err := scope.Finish(nil); err != nil {
					t.Fatal(err)
				}
			}
			lockState := e.locks.Inspect()
			for _, entry := range lockState.Resources {
				if len(entry.Pending) != 0 {
					t.Error("refresh leaked pending lock")
				}
			}
		})
	}
}

func TestMaterializedRefreshQueuedReplacement(t *testing.T) {
	for _, noData := range []bool{false, true} {
		name := "data_data"
		if noData {
			name = "no_data_data"
		}
		t.Run(name, func(t *testing.T) {
			e := New()
			d := refreshUnitRelation(t, e)
			original := e.storage[d.address]
			first := &parser.RefreshMaterializedView{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: d.name}}}, NoData: noData}
			second := &parser.RefreshMaterializedView{Name: first.Name}
			a := refreshUnitScope(t, e, context.Background())
			if err := a.acquireMaterializedRefresh(first); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			b := refreshUnitScope(t, e, ctx)
			done := make(chan error, 1)
			go func() { done <- b.acquireMaterializedRefresh(second) }()
			joined := false
			defer func() {
				cancel()
				a.Finish(nil)
				if !joined {
					lockAwait(t, done, context.Canceled)
				}
			}()
			lockPending(t, e, 1)
			before := e.commitSeq
			execute := func(scope *StatementScope, stmt *parser.RefreshMaterializedView) {
				t.Helper()
				e.mu.Lock()
				auth, err := e.authorizationLocked(scope.op)
				if err == nil {
					err = scope.captureIdentityLocked(auth)
				}
				if err != nil {
					e.mu.Unlock()
					t.Fatal(err)
				}
				result, err := e.refreshMaterializedLocked(scope, auth, stmt, 0)
				if err != nil || result.Count != 0 || result.Rows != nil {
					t.Fatal(result, err)
				}
			}
			execute(a, first)
			afterA := e.storage[d.address]
			descriptorA := e.state.descriptors[d.address].(*relationDescriptor)
			firstCount := 1
			if noData {
				firstCount = 0
			}
			if descriptorA.populated == noData || len(afterA.versions) != firstCount || e.commitSeq != before+1 || len(original.versions) != 3 {
				t.Fatal("first refresh did not publish its exact immutable transition")
			}
			if err := a.Finish(nil); err != nil {
				t.Fatal(err)
			}
			lockAwait(t, done, nil)
			joined = true
			if b.lockedDescriptor != descriptorA || b.lockedStorage != afterA {
				t.Fatal("queued refresh did not capture grant-time store")
			}
			execute(b, second)
			if err := b.Finish(nil); err != nil {
				t.Fatal(err)
			}
			final := e.state.descriptors[d.address].(*relationDescriptor)
			if e.commitSeq != before+2 || e.storage[d.address] == afterA || len(e.storage[d.address].versions) != 1 || len(afterA.versions) != firstCount || !final.populated || final.address != d.address || final.view != d.view || final.rowType != d.rowType {
				t.Fatal("serialized refresh failed complete immutable replacement")
			}
			values := e.storage[d.address].versions[0].values
			want := refreshUnitRows(t, d, 1)[0]
			if len(values) != len(want) || values[0] != want[0] {
				t.Fatal("queued refresh final typed row differs from defining SELECT1")
			}
			lockEmpty(t, e)
		})
	}
}
