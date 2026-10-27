package schemacmds_test

import (
 "context"
 "encoding/base64"
 "encoding/json"
 "reflect"
 "testing"
 "stuntgres/internal/engine"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/core"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/extension"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/plan"
 "stuntgres/internal/engine/stored"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqltype"
)
func TestPageinspectBrinProviderIdentity(t *testing.T){
 e:=engine.New();op,done:=enginetest.RoleOperation(t,e,"postgres");defer done();enginetest.RoleExecute(t,e,op,"CREATE EXTENSION pageinspect")
 view:=e.CatalogRead();ext:=view.MustExtension(view.State().NameAddress(ids.NameKey{ClassID:ids.ExtensionClassID,Name:"pageinspect"}))
 if len(ext.Members)!=4 || extension.StockPageinspect.Control().Version!="1.13"{t.Fatal(ext)}
 for i,address:=range ext.Members{
  p:=view.MustProcedure(address);m:=extension.PageinspectMembers[i]
  if p.Name()!=m.Name || p.Signature!=m.Signature || p.Result!=m.Result || p.Installed!=m.Declaration || p.Volatility!=expr.Volatile || !p.Strict || core.ProcedureReturnsSet(p)!=(i>=2){t.Fatal(p)}
  for _,mutate:=range []func(*catalog.Procedure){
   func(p *catalog.Procedure){p.Strict=false},func(p *catalog.Procedure){p.Result=16},func(p *catalog.Procedure){p.ObjectRevision=0},func(p *catalog.Procedure){p.Implementation=new(extension.PageinspectProvider)},
   func(p *catalog.Procedure){p.Installed.Outputs=catalog.NewRoutineOutputs([]ids.OID{25},"page","foreign")},
  }{
   state:=catalog.CloneState(view.State());bad:=*p;mutate(&bad);state.SetDescriptor(address,&bad)
   if err:=extension.StockPageinspect.ValidateExtension(e.Catalog.Read(state),ext);err==nil{t.Fatal("forged member accepted",bad)}
  }
 }
 b:=enginetest.ExpressionBinder(t,context.Background(),e,sqltype.InputEnvironment{},nil)
 raw:=parser.FunctionExpr{Name:parser.ObjectName{Parts:[]parser.Identifier{{Text:"brin_page_type"}}},Args:[]parser.Expr{parser.LiteralExpr{Value:parser.Value{Kind:parser.StringValue,Text:"\\x"}}}}
 node,err:=b.BindFunctionProvider(raw);if err!=nil{t.Fatal(err)};n:=node.(plan.InstalledPageinspectExpr)
 if allocations:=testing.AllocsPerRun(100,func(){if err:=n.ValidateArguments(n.Args);err!=nil{panic(err)}});allocations!=0{t.Fatal("argument validation allocated",allocations)}
 invalid:=n.Pageinspect;invalid.Slots[0]=1
 if err:=invalid.ValidateArguments(invalid.Args);err==nil{t.Fatal("invalid formal slot accepted")}
 if err:=n.ValidateArguments([]expr.Expr{nil});err==nil{t.Fatal("nil operand accepted")}
 rebuilt,err:=n.Rebuild(n.Info,n.Children());if err!=nil{t.Fatal(err)};copy:=rebuilt.(plan.InstalledPageinspectExpr)
 if copy.Ref!=n.Ref || copy.Procedure!=n.Procedure || copy.Args[0]!=n.Args[0] || copy.Member!=n.Member{t.Fatal("lost immutable selected authority")}
 if err:=stored.ValidateInstalledPageinspect(copy,view);err!=nil{t.Fatal(err)}
 bad:=n;bad.Ref.Epoch++;if err:=extension.ValidatePageinspectIdentity(view,bad.Procedure,bad.Ref);err==nil{t.Fatal("foreign epoch accepted")}
 bad=n;bad.Member="brin_revmap_data";if err:=stored.ValidateInstalledPageinspect(bad,view);err==nil{t.Fatal("foreign member accepted")}
 info:=n.Info;info.RoutineReference=nil;if _,err:=n.Rebuild(info,n.Children());err==nil{t.Fatal("lost carrier accepted")}
 copy.Args[0]=nil
 if n.Args[0]==nil{t.Fatal("rebuilt operands alias original backing")}
}

func TestPageinspectBrinOwnerCodec(t *testing.T){
 record:=extension.OwnerRecord{Name:"pageinspect",Schema:"brin_part",Version:"1.13",Owner:"postgres",Members:[]extension.MemberOwner{
  {Slot:0,Schema:"brin_moved",Kind:"procedure",Name:"page_kind",Signature:"17",Owner:"pi_owner"},
  {Slot:1,Schema:"brin_moved",Kind:"procedure",Name:"brin_metapage_info",Signature:"17",Owner:"postgres"},
  {Slot:2,Schema:"brin_moved",Kind:"procedure",Name:"brin_revmap_data",Signature:"17",Owner:"postgres"},
  {Slot:3,Schema:"brin_moved",Kind:"procedure",Name:"brin_page_items",Signature:"17,2205",Owner:"postgres"},
 }}
 encode:=func(record extension.OwnerRecord)string{
  data,err:=json.Marshal(record);if err!=nil{t.Fatal(err)}
  return extension.OwnersMarker+" "+base64.StdEncoding.EncodeToString(data)
 }
 decoded,present,err:=extension.DecodeOwners(encode(record))
 if err!=nil || !present || !reflect.DeepEqual(decoded,record){t.Fatal("valid moved member owners rejected",decoded,present,err)}
 for _,tc:=range []struct{name string;mutate func(*extension.OwnerRecord)}{
  {"version",func(r *extension.OwnerRecord){r.Version="1.12"}},
  {"missing_member",func(r *extension.OwnerRecord){r.Members=r.Members[:3]}},
  {"extra_member",func(r *extension.OwnerRecord){r.Members=append(r.Members,r.Members[0])}},
  {"duplicate_slot",func(r *extension.OwnerRecord){r.Members[3].Slot=0}},
  {"negative_slot",func(r *extension.OwnerRecord){r.Members[0].Slot=-1}},
  {"foreign_slot",func(r *extension.OwnerRecord){r.Members[3].Slot=4}},
  {"wrong_signature",func(r *extension.OwnerRecord){r.Members[3].Signature="17,26"}},
  {"wrong_kind",func(r *extension.OwnerRecord){r.Members[0].Kind="type"}},
  {"missing_namespace",func(r *extension.OwnerRecord){r.Members[0].Schema=""}},
  {"missing_owner",func(r *extension.OwnerRecord){r.Members[0].Owner=""}},
 }{
  t.Run(tc.name,func(t *testing.T){
   bad:=record;bad.Members=append([]extension.MemberOwner(nil),record.Members...);tc.mutate(&bad)
   if _,present,err:=extension.DecodeOwners(encode(bad));err==nil || !present{t.Fatal("invalid owner metadata accepted",bad,present,err)}
  })
 }
}

func TestPageinspectBrinNamedValidation(t *testing.T){
 e:=engine.New();op,done:=enginetest.RoleOperation(t,e,"postgres");defer done()
 enginetest.RoleExecute(t,e,op,"CREATE EXTENSION pageinspect")
 b:=enginetest.ExpressionBinder(t,context.Background(),e,sqltype.InputEnvironment{},nil)
 b.Clause.AggregateClause="SELECT"
 raw:=parser.FunctionExpr{
  Name:parser.ObjectName{Parts:[]parser.Identifier{{Text:"brin_page_items"}}},
  Args:[]parser.Expr{
   parser.LiteralExpr{Value:parser.Value{Kind:parser.StringValue,Text:"0"}},
   parser.LiteralExpr{Value:parser.Value{Kind:parser.StringValue,Text:"\\x"}},
  },
  ArgNames:[]parser.Identifier{{Text:"index_oid"},{Text:"page"}},
 }
 bound,err:=b.BindFunctionProvider(raw);if err!=nil{t.Fatal(err)}
 call:=bound.(plan.InstalledPageinspectExpr)
 if call.Slots!=([2]uint8{1,0}){t.Fatal("written argument order lost",call.Slots)}
 if err:=call.ValidateArguments(call.Args);err!=nil{t.Fatal(err)}
 if allocations:=testing.AllocsPerRun(100,func(){if err:=call.ValidateArguments(call.Args);err!=nil{panic(err)}});allocations!=0{t.Fatal("named validation allocated",allocations)}
 if err:=call.ValidateArguments(call.Args[:1]);err==nil{t.Fatal("missing operand accepted")}
 if err:=call.ValidateArguments(append(append([]expr.Expr(nil),call.Args...),call.Args[0]));err==nil{t.Fatal("extra operand accepted")}
 if err:=call.ValidateArguments([]expr.Expr{call.Args[1],call.Args[0]});err==nil{t.Fatal("wrong operand types accepted")}
 if err:=call.ValidateArguments([]expr.Expr{nil,call.Args[1]});err==nil{t.Fatal("nil named operand accepted")}
 typeless:=call.Pageinspect;typeless.Usage=sqltype.Usage{}
 if err:=call.ValidateArguments([]expr.Expr{typeless,call.Args[1]});err==nil{t.Fatal("operand without a result type accepted")}
 invalid:=call.Pageinspect;invalid.Slots[1]=1
 if err:=invalid.ValidateArguments(invalid.Args);err==nil{t.Fatal("duplicate slot accepted")}
 invalid=call.Pageinspect;invalid.Slots[0]=2
 if err:=invalid.ValidateArguments(invalid.Args);err==nil{t.Fatal("out-of-range slot accepted")}
 borrowed:=append([]expr.Expr(nil),call.Args...)
 rebuilt,err:=call.Rebuild(call.Info,borrowed);if err!=nil{t.Fatal(err)}
 borrowed[0]=nil
 if rebuilt.(plan.InstalledPageinspectExpr).Args[0]==nil || call.Args[0]==nil{t.Fatal("rebuild retained borrowed backing")}
}
