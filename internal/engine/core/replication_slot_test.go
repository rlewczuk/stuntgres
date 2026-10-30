package core_test

import (
 "context"
 "reflect"
 "testing"
 "stuntgres/internal/engine"
 "stuntgres/internal/engine/analyze"
 "stuntgres/internal/engine/core"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/exec"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/functions"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/plan"
 "stuntgres/internal/engine/routine"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqltype"
)
func TestReplicationSlotDeclarationOwnership(t *testing.T) {
 e,other:=engine.New(),engine.New()
 for _,oid:=range []ids.OID{3779,3786,3780,4221,4220,4224,4223,4222,3878} {
  address:=ids.ObjectAddress{ClassID:ids.ProcedureClassID,ObjectID:oid}; p,ok:=e.CatalogRead().Procedure(address); q,exists:=other.CatalogRead().Procedure(address)
  if !ok || !exists || p.Builtin==nil || p.Builtin!=q.Builtin || p.Builtin!=routine.BuiltinRoutine(oid) || !p.Strict || p.Volatility!=expr.Volatile || p.Builtin.ReturnsSet || p.Builtin.Parallel!='u' || p.Builtin.Providers!=functions.CatalogRoutineProvider { t.Fatal("released immutable declaration",oid) }
  if p.OutputDeclaration()!=q.OutputDeclaration() || p.LiteralDefaults!=q.LiteralDefaults { t.Fatal("borrowed declaration backing",oid) }
  defaults:=0; if oid==3779 { defaults=2 }; if oid==3786 { defaults=3 }
  if p.LiteralDefaults.Len()!=defaults { t.Fatal("default count",oid) }
  for i:=range defaults { value:=p.LiteralDefaults.At(i); if value.SourceType!=16 || value.Text!="false" { t.Fatal("default recipe",oid,i) } }
 }
}
func TestReplicationSlotExpressionOwnership(t *testing.T) {
 e:=engine.New(); null:=parser.LiteralExpr{Value:parser.Value{Kind:parser.NullValue}}
 for _,tc:=range []struct{name string; arity int}{{"pg_create_physical_replication_slot",1},{"pg_create_logical_replication_slot",2},{"pg_drop_replication_slot",1},{"pg_copy_physical_replication_slot",2},{"pg_copy_physical_replication_slot",3},{"pg_copy_logical_replication_slot",2},{"pg_copy_logical_replication_slot",3},{"pg_copy_logical_replication_slot",4},{"pg_replication_slot_advance",2}} {
  raw:=make([]parser.Expr,tc.arity); for i:=range raw { raw[i]=null }; n:=catalogRelationsUnitBind(t,e,tc.name,raw...).(functions.ReplicationSlot)
  if err:=n.Validate(); err!=nil { t.Fatal(err) }; if !expr.AdmitsSource(n) || exec.SourceReturnsSet(n) || n.StrictNull()!=expr.StrictNone || n.PlanningFold()!=expr.FoldNever { t.Fatal("scalar/ACL protocols") }
  children:=n.Children(); rebuilt,err:=n.Rebuild(n.Info,children); if err!=nil || !analyze.SameGroupExpression(n,rebuilt) || expr.InfoOf(rebuilt).RoutineReference!=n.RoutineReference { t.Fatal("reconstruction provenance",err) }
  children[0]=nil; if n.Args[0]==nil || rebuilt.(functions.ReplicationSlot).Args[0]==nil { t.Fatal("shared operand backing") }
  r,err:=n.Render(); if err!=nil || r.Name!=tc.name || len(r.Args)!=tc.arity { t.Fatal("render",err) }; capture,ok,err:=n.Capture(expr.CaptureFunction); if err!=nil || !ok || capture.Count!=tc.arity { t.Fatal("capture",err) }
  for i:=range tc.arity { got,err:=capture.At(n,i); if err!=nil || got!=n.Args[i] { t.Fatal("capture occurrence",err) } }
  bad:=n; bad.Kind=255; if bad.Validate()==nil { t.Fatal("invalid kind accepted") }; bad=n; bad.Slots[0]=5; if bad.Validate()==nil { t.Fatal("invalid formal accepted") }; bad=n; bad.Args[4]=n.Args[0]; if bad.Validate()==nil { t.Fatal("unused operand accepted") }
 }
 binder:=newTestExpressionBinder(t,context.Background(),e,sqltype.InputEnvironment{},nil)
 raw:=parser.FunctionExpr{
  Name:parser.ObjectName{Parts:[]parser.Identifier{{Text:"pg_create_physical_replication_slot"}}},
  Args:[]parser.Expr{parser.LiteralExpr{Value:parser.Value{Kind:parser.BooleanValue,Boolean:true}},null},
  ArgNames:[]parser.Identifier{{Text:"temporary"},{Text:"slot_name"}},
 }
 call,err:=binder.BindFunctionProvider(raw); if err!=nil { t.Fatal(err) }
 named,ok:=call.(functions.ReplicationSlot)
 if !ok || named.Arity!=2 || named.Slots!=([5]uint8{2,0}) || named.Labels!=([5]string{"temporary","slot_name"}) { t.Fatal("written/formal named mapping",call) }
 capture,ok,err:=named.Capture(expr.CaptureFunction); if err!=nil || !ok { t.Fatal("named capture",err) }
 rendering,err:=named.Render(); if err!=nil { t.Fatal(err) }
 for i:=range int(named.Arity) {
  captured,err:=capture.At(named,i)
  if err!=nil || captured!=named.Args[i] || rendering.Args[i].Expr!=captured || rendering.Args[i].Label!=named.Labels[i] { t.Fatal("named capture/render reordered operands",i,err) }
 }
}
func TestReplicationSlotRuntimeNoPublication(t *testing.T) {
 e:=engine.New(); session:=enginetest.StartSession(t,e,nil)
 op,err:=session.Acquire(context.Background()); if err!=nil { t.Fatal(err) }; defer op.Done()
 scope,err:=e.StartStatement(op); if err!=nil { t.Fatal(err) }; defer scope.Finish(context.Canceled)
 c:=&exec.Context{EvalStatement:exec.EvalStatement{Context:context.Background(),Host:core.HostOf(scope)}}
 name,_:=e.CatalogRead().LookupType(sqltype.Name); boolean,_:=e.CatalogRead().LookupType(sqltype.Bool)
 null:=parser.LiteralExpr{Value:parser.Value{Kind:parser.NullValue}}
 n:=catalogRelationsUnitBind(t,e,"pg_create_physical_replication_slot",null,null,null).(functions.ReplicationSlot)
 order:=[]int{}
 for i,v:=range []sqltype.Value{sqltype.String(name,"rsl_unit"),sqltype.Boolean(boolean,false),sqltype.Boolean(boolean,true)} { n.Args[i]=fileListOperand{plan.LiteralExpr{Info:expr.Info{Usage:v.Type().Usage()},Value:v},&order,i} }
 before:=e.CatalogRead().State()
 _,err=n.Eval(c); logicalDecodingUnitError(t,err,"0A000","pg_create_physical_replication_slot physical replication slots and WAL reservation are unsupported in embedded mode")
 if before!=e.CatalogRead().State() || !reflect.DeepEqual(order,[]int{0,1,2}) { t.Fatal("failure published state or reordered operands") }
 n.Args[0]=plan.LiteralExpr{Info:expr.Info{Usage:name.Usage()},Value:sqltype.Null(name)}
 value,err:=n.Eval(c); if err!=nil || !value.IsNull() || value.Type()!=n.ResultUsage().Type { t.Fatal("typed strict NULL",value,err) }
 if before!=e.CatalogRead().State() { t.Fatal("NULL published state") }
}
