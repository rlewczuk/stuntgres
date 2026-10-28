package core_test

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestAlterSystemEligibility(t *testing.T) {
	stmt := &parser.AlterSystem{Action:"RESET ALL"}
	if session.ModifyingTag(stmt) != "" || !session.ExplicitUtility(stmt) || session.SnapshotStatement(stmt) || session.TransactionalSchemaUtility(stmt) || session.DescribeBeforeSnapshot(stmt) != session.DescribeEmpty { t.Fatal("external utility classification") }
	if tag,_,_,ok := session.ClassifyCommandTag(stmt,session.TxIdle); !ok || tag != "ALTER SYSTEM" { t.Fatal(tag,ok) }
	for _,tc := range []struct{ block,internal,routine bool; message string }{
		{false,false,false,"ALTER SYSTEM is not allowed in this environment"},
		{false,false,true,"ALTER SYSTEM cannot be executed from a function"},
		{false,true,false,"ALTER SYSTEM cannot run inside a transaction block"},
		{false,true,true,"ALTER SYSTEM cannot run inside a transaction block"},
		{true,true,true,"ALTER SYSTEM cannot run inside a transaction block"},
	} {
		t.Run(tc.message, func(t *testing.T) {
			e := engine.New()
			sess := sessionStart(t,e,nil)
			op,err := sess.Acquire(context.Background())
			if err != nil { t.Fatal(err) }
			defer op.Done()
			scope,err := e.StartStatement(op)
			if err != nil { t.Fatal(err) }
			defer func() {
				if err := scope.Finish(nil); err != nil { t.Error(err) }
			}()
			// Admit the real implicit owner before projecting only the eligibility
			// inputs. Synthetic block state must never enter Gate or Finish.
			description,err := scope.Describe(stmt)
			if err != nil || !reflect.DeepEqual(description,core.Description{}) { t.Fatal("effect-free description",description,err) }
			before, marks := scope.Owner.State, scope.Owner.Savepoints
			defer func() {
				scope.Owner.State,scope.Owner.Savepoints = before,marks
			}()
			if tc.block { scope.Owner.State = session.TxActive }
			if tc.internal { scope.Owner.Savepoints = []session.SavepointMark{{Internal:true}} }
			if tc.routine {
				if err := core.ScopeEnterSQLRoutine(scope); err != nil { t.Fatal(err) }
				defer core.ScopeLeaveSQLRoutine(scope)
			}
			_,err = core.EngineExecuteStatement(e,scope,stmt,0,nil)
			var d *sqlstate.Error
			code := "25001"
			if !tc.block && !tc.internal && !tc.routine { code = "0A000" }
			if !errors.As(err,&d) || d.Code != code || d.Message != tc.message { t.Fatalf("eligibility %+v: %v",tc,err) }
		})
	}
}
