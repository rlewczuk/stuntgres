package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestConcurrentIndexU5LiveSimpleRowKeyBitmapIndependentOfReadiness(t *testing.T) {
	for _, phase := range []string{"nonready", "readyinvalid", "valid"} {
		for _, shape := range []string{"simple", "parenthesized", "expression", "mixed"} {
			t.Run(phase+"/"+shape, func(t *testing.T) {
				e := New()
				owner, builder, consumer := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
				sessionSQL(t, owner, "CREATE TABLE t(n INT,p INT)")
				sessionSQL(t, owner, "CREATE TABLE u(n INT)")
				sessionSQL(t, owner, "INSERT INTO t VALUES(1,10)")
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
					sessionSQL(t, owner, "BEGIN ISOLATION LEVEL READ COMMITTED")
					sessionSQL(t, owner, "INSERT INTO t VALUES(3,30)")
					cancel, await = indexMaintenanceAsync(t, e, builder, query)
					concurrentUnitCompletion(t, e, owner.Transactions.Ticket, builder)
				case "readyinvalid":
					sessionSQL(t, owner, "BEGIN ISOLATION LEVEL REPEATABLE READ")
					rows := sessionSQL(t, owner, "SELECT n FROM u")
					rows.Rows.Close()
					cancel, await = indexMaintenanceAsync(t, e, builder, query)
					concurrentUnitCompletion(t, e, owner.Transactions.Ticket, builder)
				case "valid":
					sessionSQL(t, builder, query)
				}
				idx, _ := concurrentUnitIndex(t, e, "ui", phase != "nonready", phase == "valid")
				scope, op, _ := concurrentUnitSnapshotScope(t, e, consumer)
				ref := roleTableRef(t, e, "t")
				binding, err := dml.BindUniqueWrite(scope.writeHost(), ref)
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
				e.mu.Lock()
				parent := e.catalog.state.MustRelation(ref.Address)
				old := e.storage.Relations[ref.Address].Versions[0].Values
				e.mu.Unlock()
				next := append([]sqltype.Value(nil), old...)
				next[0], err = sqltype.Integer(parent.Columns[0].Usage.Type, 2)
				if err != nil {
					t.Fatal(err)
				}
				changed, err := dml.UniqueKeyChanged(scope.writeHost(), ref, old, next)
				if err != nil || changed != simple {
					t.Fatal("computed dependencies leaked into UPDATE strength", changed, err)
				}
				next = append([]sqltype.Value(nil), old...)
				next[1], err = sqltype.Integer(parent.Columns[1].Usage.Type, 20)
				if err != nil {
					t.Fatal(err)
				}
				changed, err = dml.UniqueKeyChanged(scope.writeHost(), ref, old, next)
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
					sessionSQL(t, owner, "ROLLBACK")
				}
				lockEmpty(t, e)
			})
		}
	}
}
