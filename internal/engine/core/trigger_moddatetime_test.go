package core_test

import (
 "context"
 "errors"
 "testing"
 "time"
 "stuntgres/internal/engine"
 "stuntgres/internal/engine/core"
 "stuntgres/internal/engine/dml"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/sqlstate"
 "stuntgres/internal/sqltype"
)

func TestModdatetimeTriggerBoundary(t *testing.T) {
 for _,typ:=range []string{"timestamp(0)","timestamp(3)","timestamptz(0)","timestamptz(3)"} {for _,zone:=range []string{"UTC","Europe/Helsinki"} {t.Run(typ+"/"+zone,func(t *testing.T){
  e:=engine.New();setup,finish:=enginetest.RoleOperation(t,e,"postgres")
  for _,q:=range []string{"CREATE EXTENSION moddatetime","CREATE TABLE mdt(id integer,stamp "+typ+")","CREATE TRIGGER a BEFORE INSERT OR UPDATE ON mdt FOR EACH ROW EXECUTE FUNCTION moddatetime('stamp')"} {enginetest.RoleExecute(t,e,setup,q)}
  finish()
  ctx,cancel:=context.WithCancel(context.Background());defer cancel();op,done,err:=enginetest.Operation(e,ctx);if err!=nil {t.Fatal(err)};defer done()
  enginetest.RoleExecute(t,e,op,"SET TimeZone='"+zone+"'")
  s,err:=e.StartStatement(op);if err!=nil {t.Fatal(err)};defer s.Finish(nil)
  read:=e.CatalogRead();ns:=read.NameAddress(ids.NameKey{ClassID:ids.NamespaceClassID,Name:"public"});r:=read.MustRelation(read.NameAddress(ids.NameKey{ClassID:ids.RelationClassID,Namespace:ns,Name:"mdt"}))
  triggerUnitAdmit(t,s,r)
  s.Input.TransactionStart=time.Date(2001,2,3,4,5,6,123456000,time.UTC)
  integer:=r.Columns[0].Usage.Type;temporal:=r.Columns[1].Usage.Type
  old:=[]sqltype.Value{triggerUnitInt(t,integer,1),sqltype.Null(temporal)}
  next:=[]sqltype.Value{old[0],old[1]};ref:=read.Owner().Domain.Ref(read.Epoch(),r.Address())
  h:=s.WriteHost();h.EnterForeignKeyTrigger();defer h.LeaveForeignKeyTrigger()
  inputZone,inputStyle:=s.Input.Zone,s.Input.DateStyle
  callerDemand:=core.ScopeSQLRoutine(s)
  balanced:=func(){depth,err:=h.TriggerDepthValue(integer);n,_:=depth.Integer();if err!=nil||n!=1||core.ScopeActiveSQLRoutineDepth(s)!=0 {t.Fatal("unbalanced installed native invocation",n,err)};if s.Input.Zone!=inputZone||s.Input.DateStyle!=inputStyle||!core.SameSQLRoutineDemand(callerDemand,core.ScopeSQLRoutine(s)) {t.Fatal("routine environment or command policy leaked")}}
  result,err:=h.BeforeRow(dml.BeforeRow{Relation:ref,Event:dml.BeforeUpdate,Old:old,New:next})
  if err!=nil||!result.Proceed||len(result.Row)!=2 {t.Fatal(result,err)}
  if &result.Row[0]!=&next[0]||!old[1].IsNull() {t.Fatal("copied NEW or mutated OLD")}
  text,err:=temporal.Output(context.Background(),result.Row[1]);want:="2001-02-03 04:05:06.123456";if zone!="UTC"&&temporal.ID()==sqltype.Timestamp {want="2001-02-03 06:05:06.123456"};if temporal.ID()==sqltype.Timestamptz {want+="+00"};if err!=nil||text!=want {t.Fatal(text,want,err)}
  balanced()
  captured:=result.Row[1]
  other:=[]sqltype.Value{triggerUnitInt(t,integer,2),sqltype.Null(temporal)}
  if _,err:=h.BeforeRow(dml.BeforeRow{Relation:ref,Event:dml.BeforeUpdate,Old:old,New:other});err!=nil {t.Fatal(err)}
  if output,_:=temporal.Output(context.Background(),captured);output!=want {t.Fatal("later invocation mutated retained datum")}
  _,err=h.BeforeRow(dml.BeforeRow{Relation:ref,Event:dml.BeforeInsert,New:other})
  var diagnostic *sqlstate.Error;if !errors.As(err,&diagnostic)||diagnostic.Code!="XX000"||diagnostic.Message!="moddatetime: cannot process INSERT events" {t.Fatal("installed helper error",err)}
  balanced()
  cancel();before:=other[1]
  _,err=h.BeforeRow(dml.BeforeRow{Relation:ref,Event:dml.BeforeUpdate,Old:old,New:other})
  if !errors.Is(err,context.Canceled) {t.Fatal("canceled installed helper",err)}
  balanced();a,_:=temporal.Output(context.Background(),before);b,_:=temporal.Output(context.Background(),other[1]);if a!=b {t.Fatal("cancellation changed NEW")}
 })} }
}
