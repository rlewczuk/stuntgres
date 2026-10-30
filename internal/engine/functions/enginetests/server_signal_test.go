package enginetests_test

import (
 "reflect"
 "slices"
 "testing"
 "stuntgres/internal/engine/exec"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/functions"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/routine"
 "stuntgres/internal/sqltype"
)
func TestServerSignalTypedRecipes(t *testing.T){
 e,op,scope:=strictNullUnitFixture(t)
 if got:=slices.Collect(routine.RestrictedServerSignalRoutines());!slices.Equal(got,[]ids.OID{2621,2622,4543}){t.Fatal(got)}
 for i,call:=range []string{"pg_reload_conf()","pg_rotate_logfile()","pg_log_backend_memory_contexts(NULL)"}{
  node,_,evaluation:=strictNullUnitBind(t,e,op,scope,call);n:=node.(functions.ServerSignal)
  d,err:=exec.ExpressionRoutineDescriptor(n);if err!=nil||d==nil||d.Address().ObjectID!=[]ids.OID{2621,2622,4543}[i]||d.Result!=16||!d.Strict||d.Volatility!=expr.Volatile||d.Builtin.Parallel!='s'||d.ReturnsSet(){t.Fatal(d,err)}
  if !d.ACL.Explicit||len(d.ACL.Entries)!=1{t.Fatal("restricted seed",d.ACL)}
  if n.Validate()!=nil||n.Usage.Type.ID()!=sqltype.Bool||n.PlanningFold()!=expr.FoldNever||n.PreflightFold()!=expr.PreflightNullBare||n.StrictNull()!=expr.StrictAfterBare{t.Fatal(n)}
  before:=n;info:=n.Info;info.Occurrence++
  next,err:=n.Rebuild(info,n.Children());if err!=nil||next.(functions.ServerSignal).RoutineReference!=n.RoutineReference||!reflect.DeepEqual(before,n){t.Fatal(next,err)}
  if _,err:=n.Render();err!=nil{t.Fatal(err)}
  capture,ok,err:=n.Capture(expr.CaptureFunction);if err!=nil||!ok||capture.Count!=len(n.Children()){t.Fatal(capture,ok,err)}
  bad:=n;bad.Kind=255;if bad.Validate()==nil{t.Fatal("invalid kind accepted")}
  if i==2{v,err:=n.Eval(&evaluation);if err!=nil||!v.IsNull()||v.Type()!=n.Usage.Type{t.Fatal(v,err)};children:=n.Children();children[0]=nil;if n.Validate()!=nil{t.Fatal("child slice mutated immutable recipe")};if _,err:=n.Rebuild(info,children);err==nil{t.Fatal("nil child accepted")}}
 }
}
