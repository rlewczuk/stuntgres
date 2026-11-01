package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestSnapshotSyncActualOwnership(t *testing.T) {
	for _,cancelled:=range []bool{false,true}{
		e:=engine.New();enginetest.Run(t,e,"CREATE TABLE sync_actual_ownership(id INT)")
		s:=snapshotSyncScope(t,e,false,session.ReadCommitted)
		actual:=enginetest.Statement(t,"SELECT pg_export_snapshot()").(*parser.Select).Targets[0]
		formal:=analyze.ParameterInfo{Count:1,Types:[]sqltype.Usage{enginetest.Usage(e,sqltype.Text)}}
		frame:=make([]sqltype.Value,1)
		manifest:=s.RoutineDependencies
		ctx:=s.Op.Ctx
		if cancelled{var cancel context.CancelFunc;ctx,cancel=context.WithCancel(ctx);cancel()}
		err:=core.ScopeSQLExecuteImporter(s,&parser.ExecuteSQL{Name:parser.Identifier{Text:"actual"},Arguments:[]parser.Expr{actual}},false)(ctx,formal,frame,s.Input)
		if cancelled{
			if !errors.Is(err,context.Canceled)||core.EngineExportCount(e)!=0{t.Fatal("cancelled actual exported",err)}
		}else{
			if err!=nil{t.Fatal(err)}
			token:=enginetest.Export(frame[0]).(string)
			roots,_,cut:=core.EngineExportCapture(e,token)
			if roots==nil{t.Fatal("actual did not own retained roots")}
			if current,_,_,_:=core.ScopeExportCapture(s);current!=nil||core.ScopeCurrentSnapshot(s)!=nil{t.Fatal("actual replaced body active-snapshot ownership")}
			enginetest.Run(t,e,"INSERT INTO sync_actual_ownership VALUES(1)")
			core.SetScopeExportRootDemand(s,true)
			if err:=core.ScopeSnapshotDataPhase(s);err!=nil{t.Fatal(err)}
			body,_,_,frontier:=core.ScopeExportCapture(s)
			if frontier<=cut||reflect.ValueOf(body).Pointer()==reflect.ValueOf(roots).Pointer(){t.Fatal("body reused actual's old RC capture")}
		}
		if !reflect.DeepEqual(manifest,s.RoutineDependencies){t.Fatal("actual replaced body dependency manifest")}
	}
}
