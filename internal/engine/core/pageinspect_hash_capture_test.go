package core_test

import (
 "encoding/binary"
 "testing"
 "stuntgres/internal/engine"
 "stuntgres/internal/engine/core"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/exec"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/plan"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqltype"
)
func TestPageinspectHashLiveCaptureAuthority(t *testing.T){
 e:=engine.New();s:=enginetest.StartSession(t,e,nil);enginetest.SessionSQL(t,s,"CREATE EXTENSION pageinspect");scope:=pgrowlocksUnitScope(t,e,s)
 for i,name:=range []string{"hash_page_type","hash_page_stats","hash_page_items","hash_metapage_info"}{
  raw:=parser.FunctionExpr{Name:parser.ObjectName{Parts:[]parser.Identifier{{Text:name}}},Args:[]parser.Expr{parser.LiteralExpr{Value:parser.Value{Kind:parser.StringValue,Text:"\\x"}}}}
  e.Mu.Lock();auth,err:=core.ScopeWriteAuthorizationLocked(scope,e);var bound expr.Expr
  if err==nil{b:=core.NewExpressionBinder(scope.Op.Context(),e,scope.Input,scope.Op,nil);b.Query.Auth=&auth;b.Clause.AggregateClause="SELECT";bound,err=b.BindFunctionProvider(raw);if err==nil{scope.RoutineAuth,scope.RoutineDependencies=auth,b.Dependencies}}
  e.Mu.Unlock();if err!=nil{t.Fatal(err)};call:=bound.(plan.InstalledPageinspectExpr)
  if _,err:=scope.WriteHost().Pageinspect(call,0);err!=nil{t.Fatal("hash live capture",err)}
  evaluation:=&exec.Context{EvalStatement:exec.EvalStatement{Context:scope.Op.Context(),Catalog:auth.Catalog,Host:core.HostOf(scope),Input:scope.Input,Output:scope.Output}}
  page:=make([]byte,8192);binary.LittleEndian.PutUint16(page[12:],28);binary.LittleEndian.PutUint16(page[14:],8160);binary.LittleEndian.PutUint16(page[16:],8176);binary.LittleEndian.PutUint16(page[18:],0x2004);binary.LittleEndian.PutUint16(page[8188:],2);binary.LittleEndian.PutUint16(page[8190:],0xff80)
  binary.LittleEndian.PutUint32(page[24:],8160|1<<15|16<<17);binary.LittleEndian.PutUint16(page[8160:],1);binary.LittleEndian.PutUint16(page[8162:],2);binary.LittleEndian.PutUint16(page[8164:],513);binary.LittleEndian.PutUint16(page[8166:],16);binary.LittleEndian.PutUint32(page[8168:],0xffffffff)
  if i==3{binary.LittleEndian.PutUint16(page[8188:],8);binary.LittleEndian.PutUint32(page[24:],0x06440640);binary.LittleEndian.PutUint32(page[28:],4);binary.LittleEndian.PutUint32(page[72:],4000000000);binary.LittleEndian.PutUint32(page[76:],7)}
  evaluate:=func()sqltype.Value{t.Helper();usage:=call.Args[0].ResultUsage();input,err:=sqltype.Binary(usage.Type,string(page));if err!=nil{t.Fatal(err)};arg:=plan.LiteralExpr{Info:expr.Info{Usage:usage},Value:input};next,err:=call.Rebuild(call.Info,[]expr.Expr{arg});if err!=nil{t.Fatal(err)};n:=next.(plan.InstalledPageinspectExpr)
   if n.Ref!=call.Ref||n.Procedure!=call.Procedure||n.Args[0]==call.Args[0]{t.Fatal("hash rebuild lost selected ownership")}
   if i==2{cursor,err:=evaluation.OpenPageinspect(n);if err!=nil{t.Fatal(err)};defer cursor.Close();value,ok,err:=cursor.Next(scope.Op.Context());if err!=nil||!ok{t.Fatal(value,ok,err)};return value}
   value,err:=n.Eval(evaluation);if err!=nil{t.Fatal(err)};return value
  }
  first:=evaluate();text,err:=first.Type().Output(scope.Op.Context(),first);if err!=nil{t.Fatal(err)}
  switch i{case 0:binary.LittleEndian.PutUint16(page[8188:],1);case 1:binary.LittleEndian.PutUint16(page[12:],24);case 2:binary.LittleEndian.PutUint32(page[8168:],42);case 3:binary.LittleEndian.PutUint32(page[76:],9)}
  second:=evaluate();changed,err:=second.Type().Output(scope.Op.Context(),second);if err!=nil||changed==text{t.Fatal("hash changed input did not distinguish results",text,changed,err)}
  clear(page);retained,err:=first.Type().Output(scope.Op.Context(),first);if err!=nil||retained!=text||first.Type()!=call.ResultUsage().Type{t.Fatal("hash owned output changed",retained,text,err)}
  if i!=0{for j:=range first.Type().CompositeLen(){if first.CompositeField(j).Type()!=call.ResultUsage().Type.CompositeMember(j).Usage.Type{t.Fatal("hash output descriptor lost",j)}}}
  value,err:=call.Args[0].Eval(evaluation);if err!=nil{t.Fatal(err)};if raw,err:=value.Binary();err!=nil||raw!=""{t.Fatal("hash predecessor operands changed",raw,err)}
 }
}
