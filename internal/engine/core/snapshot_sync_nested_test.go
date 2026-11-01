package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/session"
)

func TestSnapshotSyncRoutineSnapshotOwnership(t *testing.T){
	for _,isolation:=range []session.Isolation{session.ReadCommitted,session.RepeatableRead}{
		for _,volatility:=range []expr.Volatility{expr.Stable,expr.Volatile}{
			for _,unwind:=range []error{nil,errors.New("reached routine error"),context.Canceled}{
				e:=engine.New();enginetest.Run(t,e,"CREATE TABLE sync_nested(id INT)");enginetest.Run(t,e,"INSERT INTO sync_nested VALUES(1)")
				s:=snapshotSyncScope(t,e,true,isolation);if err:=core.ScopeSnapshotDataPhase(s);err!=nil{t.Fatal(err)}
				outer,_,outerValue,outerFrontier:=core.ScopeExportCapture(s)
				enginetest.Run(t,e,"INSERT INTO sync_nested VALUES(2)")
				leave:=core.ActivateRoutineCommandPolicy(s,&catalog.Procedure{Volatility:volatility},false)
				restore:=s.ActivateSQLRoutine(nil,e.CatalogRead())
				if err:=core.ScopeSnapshotDataPhase(s);err!=nil{t.Fatal(err)}
				child,_,childValue,childFrontier:=core.ScopeExportCapture(s)
				fresh:=isolation==session.ReadCommitted&&volatility==expr.Volatile
				if fresh {
					if childFrontier<=outerFrontier||childValue==outerValue||reflect.ValueOf(child).Pointer()==reflect.ValueOf(outer).Pointer(){t.Fatal("volatile child reused outer active snapshot")}
				} else if childFrontier!=outerFrontier||childValue!=outerValue||reflect.ValueOf(child).Pointer()!=reflect.ValueOf(outer).Pointer(){t.Fatal("readonly/stable child failed to borrow outer cut")}
				if _,err:=core.ScopeExportSnapshot(s);err!=nil{t.Fatal(err)}
				if err:=restore(unwind);err!=unwind{t.Fatalf("unwind changed primary: %v",err)};leave()
				after,_,value,frontier:=core.ScopeExportCapture(s)
				if frontier!=outerFrontier||value!=outerValue||reflect.ValueOf(after).Pointer()!=reflect.ValueOf(outer).Pointer(){t.Fatal("balanced unwind did not restore exact outer active capture")}
			}
		}
	}
}
