package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
)

// Exercise real relation admission with composed descriptors, not a SQL fixture:
// a parent-only cohort holder must not also block pending-child rename on AX.
func TestPartitionDetachConcurrentOnlyAdmission(t *testing.T) {
	for _, only := range []bool{false, true} {
		t.Run(map[bool]string{false: "recursive", true: "only"}[only], func(t *testing.T) {
			e, parent, mid, source, destination := partitionRoutingUnitEngine(t)
			op, done, err := enginetest.Operation(e, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(done)
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if !scope.Finished {
					scope.Finish(errors.New("abandoned ONLY admission witness"))
				}
			})
			syntax := &parser.Select{From: &parser.TableReference{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: parent.Name()}}}, Only: only}}
			if err := scope.Gate(syntax); err != nil {
				t.Fatal(err)
			}
			scope.Statement = syntax
			if err := core.ScopeAcquireRelationTarget(scope, syntax, syntax, true, false); err != nil {
				t.Fatal(err)
			}
			snapshot := e.Locks.Inspect()
			for i, address := range []struct {
				ref  lock.Resource
				want bool
			}{
				{lock.Resource{ObjectRef: e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), parent.Address())}, true},
				{lock.Resource{ObjectRef: e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), mid.Address())}, !only},
				{lock.Resource{ObjectRef: e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), source.Address())}, !only},
				{lock.Resource{ObjectRef: e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), destination.Address())}, !only},
			} {
				held := false
				if entry := snapshot.Resources[address.ref]; entry != nil {
					held = entry.Holders[scope.LockTicket].Mode&lock.AccessShare != 0
				}
				if held != address.want {
					t.Fatalf("relation %d AccessShare=%v, want %v", i, held, address.want)
				}
			}
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}
