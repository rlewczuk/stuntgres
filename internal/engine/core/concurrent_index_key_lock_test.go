package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func TestConcurrentIndexU5LiveSimpleRowKeyBitmapIndependentOfReadiness(t *testing.T) {
	for _, phase := range []string{"nonready", "readyinvalid", "valid"} {
		for _, shape := range []string{"simple", "parenthesized", "expression", "mixed"} {
			t.Run(phase+"/"+shape, func(t *testing.T) {
				e := engine.New()
				owner, builder, consumer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, owner, "CREATE TABLE t(n INT,p INT)")
				enginetest.SessionSQL(t, owner, "CREATE TABLE u(n INT)")
				enginetest.SessionSQL(t, owner, "INSERT INTO t VALUES(1,10)")
				definition := "n) INCLUDE(p)"
				if shape == "parenthesized" {
					definition = "(n)) INCLUDE(p)"
				}
				if shape == "expression" {
					definition = "(n+1)) INCLUDE(p)"
				}
				if shape == "mixed" {
					definition = "(n+1),p)"
				}
				query := "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(" + definition
				var cancel context.CancelFunc
				var await func() error
				switch phase {
				case "nonready":
					enginetest.SessionSQL(t, owner, "BEGIN ISOLATION LEVEL READ COMMITTED")
					enginetest.SessionSQL(t, owner, "INSERT INTO t VALUES(3,30)")
					cancel, await = indexMaintenanceAsync(t, e, builder, query)
					concurrentUnitCompletion(t, e, owner.Transactions.Ticket, builder)
				case "readyinvalid":
					enginetest.SessionSQL(t, owner, "BEGIN ISOLATION LEVEL REPEATABLE READ")
					rows := enginetest.SessionSQL(t, owner, "SELECT n FROM u")
					rows.Rows.Close()
					cancel, await = indexMaintenanceAsync(t, e, builder, query)
					concurrentUnitCompletion(t, e, owner.Transactions.Ticket, builder)
				case "valid":
					enginetest.SessionSQL(t, builder, query)
				}
				idx, _ := concurrentUnitIndex(t, e, "ui", phase != "nonready", phase == "valid")
				scope, op, _ := concurrentUnitSnapshotScope(t, e, consumer)
				ref := enginetest.RoleTableRef(t, e, "t")
				binding, err := dml.BindUniqueWrite(scope.WriteHost(), ref)
				if err != nil {
					t.Fatal(err)
				}
				simple := shape == "simple" || shape == "parenthesized"
				expected := []int(nil)
				if simple {
					expected = []int{0}
				}
				if !reflect.DeepEqual(binding.KeySlots, expected) {
					t.Fatal("row-key bitmap conflated enforcement, INCLUDE or mixed expression keys", binding.KeySlots, expected)
				}
				wantedRules := 1
				if phase == "nonready" {
					wantedRules = 0
				}
				if len(binding.Keys) != wantedRules {
					t.Fatal("enforcement ignored Ready boundary", len(binding.Keys), wantedRules)
				}
				e.Mu.Lock()
				parent := e.Catalog.State.MustRelation(ref.Address)
				old := e.Storage.Relations[ref.Address].Versions[0].Values
				e.Mu.Unlock()
				next := append([]sqltype.Value(nil), old...)
				next[0], err = sqltype.Integer(parent.Columns[0].Usage.Type, 2)
				if err != nil {
					t.Fatal(err)
				}
				changed, err := dml.UniqueKeyChanged(scope.WriteHost(), ref, old, next)
				if err != nil || changed != simple {
					t.Fatal("computed dependencies leaked into UPDATE strength", changed, err)
				}
				next = append([]sqltype.Value(nil), old...)
				next[1], err = sqltype.Integer(parent.Columns[1].Usage.Type, 20)
				if err != nil {
					t.Fatal(err)
				}
				changed, err = dml.UniqueKeyChanged(scope.WriteHost(), ref, old, next)
				if err != nil || changed {
					t.Fatal("INCLUDE or mixed simple component strengthened UPDATE", changed, err)
				}
				// Key extraction still sees all expression/simple components independently
				// of the native row-key bitmap classification.
				ordering, err := index.BindIndexOrdering(operatorFamilyTestRead(), parent, idx.SQLIndex)
				if err != nil {
					t.Fatal(err)
				}
				before, err := ordering.Entry(storage.RowVersion{Values: old}, &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}})
				if err != nil {
					t.Fatal(err)
				}
				after, err := ordering.Entry(storage.RowVersion{Values: next}, &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}})
				if err != nil {
					t.Fatal(err)
				}
				comparison, err := ordering.CompareKeys(context.Background(), before, after)
				if err != nil || (comparison != 0) != (shape == "mixed") {
					t.Fatal("actual tuple lost mixed key component", comparison, err)
				}
				if err := scope.Finish(nil); err != nil {
					t.Fatal(err)
				}
				op.Done()
				if cancel != nil {
					cancel()
					if err := await(); !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
					enginetest.SessionSQL(t, owner, "ROLLBACK")
				}
				enginetest.LockEmpty(t, e)
			})
		}
	}
}

func concurrentUnitSnapshotScope(t *testing.T, e *core.Engine, s *session.Session) (*core.StatementScope, *session.Operation, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	op, err := s.Acquire(ctx)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		cancel()
		op.Done()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		if !scope.Finished {
			scope.Finish(context.Canceled)
		}
		op.Done()
	})
	if err := scope.Gate(enginetest.Statement(t, "SELECT 1")); err != nil {
		t.Fatal(err)
	}
	return scope, op, cancel
}
