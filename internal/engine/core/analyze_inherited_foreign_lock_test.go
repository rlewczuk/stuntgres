package core_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
)

func TestAnalyzeInheritedForeignDescendantLock(t *testing.T) {
	for _, partition := range []bool{false, true} {
		for _, cancelled := range []bool{false, true} {
			t.Run(map[bool]string{false: "ordinary", true: "partition"}[partition]+map[bool]string{false: "/resume", true: "/cancel"}[cancelled], func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					f := newInheritedForeignFixture(t, 10, partition)
					f.wrapper.sources["mixed_foreign"] = mixedForeignSource(1, 30, 30)
					enginetest.SessionSQL(t, f.s, "CREATE STATISTICS mixed_wait(ndistinct,dependencies,mcv) ON a,b FROM mixed_parent")
					f.analyze(t, false, "")
					before := f.e.Storage.RelationMetadata[f.parent.Address]
					retained := f.retained(t)
					enginetest.SessionSQL(t, f.s, "INSERT INTO mixed_parent VALUES(1,11)")
					blocker, ticket := blockingUnitOwner(t, f.e)
					resource := lock.Resource{ObjectRef: f.foreign}
					if err := f.e.Locks.Acquire(context.Background(), ticket, resource, lock.AccessExclusive); err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					done := make(chan error, 1)
					go func() { done <- f.execute(t, ctx, "ANALYZE(SKIP_LOCKED) ONLY mixed_parent", false) }()
					synctest.Wait()
					select {
					case err := <-done:
						t.Fatal("SKIP_LOCKED parent skipped or bypassed actual descendant wait", err)
					default:
					}
					state := f.e.Locks.Inspect().Resources[resource]
					if state == nil || len(state.Pending) != 1 || state.Pending[0].Mode != lock.AccessShare || state.Pending[0].Terminal {
						t.Fatal("not waiting on genuine descendant AccessShare", state)
					}
					pending := f.e.Storage.RelationMetadata[f.parent.Address]
					if pending.Statistics != before.Statistics || pending.InheritedTuples != 40 {
						t.Fatal("pending inherited wait published typed products")
					}
					if partition {
						if pending != before {
							t.Fatal("partition wait fabricated local completion")
						}
					} else if pending.AnalyzeCount != before.AnalyzeCount+1 || f.e.Storage.Relations[f.parent.Address].Maintenance.Tuples != 11 {
						t.Fatal("local pass did not complete before inherited wait")
					}
					if cancelled {
						cancel()
					} else {
						enginetest.SessionSQL(t, blocker, "ROLLBACK")
					}
					err := <-done
					if cancelled {
						if !errors.Is(err, context.Canceled) {
							t.Fatal("descendant cancellation changed", err)
						}
						if f.e.Storage.RelationMetadata[f.parent.Address] != pending {
							t.Fatal("cancelled wait fabricated inherited publication or retracted local completion")
						}
						enginetest.SessionSQL(t, blocker, "ROLLBACK")
					} else {
						if err != nil {
							t.Fatal(err)
						}
						after := f.e.Storage.RelationMetadata[f.parent.Address]
						if after.InheritedTuples != 41 || after.Statistics == before.Statistics || after.AnalyzeCount != before.AnalyzeCount+1 {
							t.Fatal("resumed inherited pass missed genuine41 or double-counted local completion", after)
						}
					}
					retained()
					enginetest.LockEmpty(t, f.e)
				})
			})
		}
	}
}
