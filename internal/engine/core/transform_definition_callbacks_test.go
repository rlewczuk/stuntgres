package core_test

import (
 "context"
 "errors"
 "testing"
 "time"

 "stuntgres/internal/engine/core"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/proc"
 "stuntgres/internal/engine/session"
 "stuntgres/internal/sqltype"
)

func TestTransformDefinitionCallbackReadmission(t *testing.T) {
 ctx,cancel:=context.WithTimeout(t.Context(),60*time.Second);defer cancel()
 var e *core.Engine
 var operation *session.Operation
 calls:=0
 e,_=transformDefinitionOwner(t,func()error{
  calls++
  if operation==nil {return errors.New("transform metadata invoked application code")}
  if !e.Mu.TryLock(){return errors.New("refreshed callback under engine mutex")};e.Mu.Unlock()
  if !e.ReplacementMu.TryLock(){return errors.New("refreshed callback under replacement reservation")};e.ReplacementMu.Unlock()
  settings:=operation.EffectiveSettings()
  if !settings.Mu.TryLock(){return errors.New("refreshed callback under settings mutex")};settings.Mu.Unlock()
  if !operation.Session.Mu.TryLock(){return errors.New("refreshed callback under session mutex")};operation.Session.Mu.Unlock()
  return nil
 })
 enginetest.Run(t,e,transformOwnerReplacement)
 if calls!=0 {t.Fatal("definition refresh executed callbacks")}
 descriptor:=checkedDefaultOwnerProcedure(t,e.CatalogRead(),"owner_transform")
 layout:=descriptor.Host.Layout
 frame:=proc.NewFrame(layout.Inputs)
 value,err:=layout.Inputs[0].Type.HstoreFromEntries(ctx,[]sqltype.HstoreEntry{{Key:"a",Value:"7"},{Key:"b",Null:true}})
 if err!=nil {t.Fatal(err)}
 if err=frame.Bind(0,value,layout.Inputs[0]);err!=nil {t.Fatal(err)}
 var done func()
 operation,done,err=enginetest.Operation(e,ctx);if err!=nil {t.Fatal(err)};defer done()
 scope,err:=e.StartStatement(operation);if err!=nil {t.Fatal(err)}
 result,err:=core.ScopeInvokeSQLRoutine(scope,descriptor,frame,layout.Result,nil,sqltype.CollationRef{},false)
 depth:=core.ScopeActiveSQLRoutineDepth(scope)
 scope.Finish(err)
 if err!=nil {t.Fatal(err)}
 if result.Type()!=layout.Result.Type || depth!=0 || calls!=4 {t.Fatal("refreshed genuine frame/callback lifetime imbalance",depth,calls)}
 if descriptor.Host.Layout!=layout || !e.Mu.TryLock() {t.Fatal("invocation changed immutable layout or leaked engine lock")};e.Mu.Unlock()
 if !e.ReplacementMu.TryLock(){t.Fatal("invocation leaked replacement reservation")};e.ReplacementMu.Unlock()
}
