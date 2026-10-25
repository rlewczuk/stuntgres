package schemacmds_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)


func citextStringUnitCall(t *testing.T,view catalog.Read,installation *catalog.Extension,base *catalog.Type,index int) functions.String {
 t.Helper();m:=extension.CitextStringMembers[index];slot:=extension.CitextStringProcedureStart+index;p:=view.MustProcedure(installation.Members[slot]);result,_:=view.LookupType(sqltype.ID(m.Result));text,_:=view.LookupType(sqltype.Text)
 args:=[]expr.Expr{
  plan.LiteralExpr{Info:expr.Info{Usage:base.SQLType.Usage()},Value:sqltype.String(base.SQLType,"AbA")},
  plan.LiteralExpr{Info:expr.Info{Usage:base.SQLType.Usage()},Value:sqltype.String(base.SQLType,"a")},
 }
 if m.ArgCount()==3 {
  typ:=base.SQLType;if !m.ThirdCitext {typ,_=view.LookupType(m.Third)}
  var value sqltype.Value
  if m.Third==sqltype.Int4 {value,_=sqltype.Integer(typ,2)} else {value=sqltype.String(typ,"X")}
  args=append(args,plan.LiteralExpr{Info:expr.Info{Usage:typ.Usage()},Value:value})
 }
 call,err:=functions.NewCitextString(expr.Info{Usage:result.Usage(),RoutineReference:p,Property:expr.Properties{Volatility:expr.Immutable}},m.Name,args,functions.CitextStringCapture{Ref:view.Owner().Domain.Ref(view.Epoch(),p.Address()),Namespace:"public",Name:p.Name(),Member:uint8(slot),Text:text.Usage()})
 if err!=nil {t.Fatal(err)};return call
}
func TestCitextStringProvider(t *testing.T) {
 e,installation,base:=citextUnitInstalled(t);view:=e.CatalogRead()
 if len(installation.Members)!=56||extension.CitextRegexpProcedureStart!=42||extension.CitextRegexpProcedureEnd!=52||extension.CitextStringProcedureStart!=52 {t.Fatal("changed incumbent manifest bounds")}
 for i,m:=range extension.CitextRegexpMembers {
  p:=view.MustProcedure(installation.Members[42+i]);symbol,err:=extension.ProcedureSymbol(view,p)
  if err!=nil||symbol!=m.Name||p.Signature!=m.Signature(base.SQLType.ID()) {t.Fatal("changed incumbent regexp",i,err)}
 }
 defaults,err:=stored.NewRoutineDefaults(context.Background(),view,nil,nil);if err!=nil {t.Fatal(err)}
 for i,m:=range extension.CitextStringMembers {
  p:=view.MustProcedure(installation.Members[52+i]);symbol,err:=extension.ProcedureSymbol(view,p)
  if err!=nil||symbol!=m.Name||p.Signature!=m.Signature(base.SQLType.ID())||p.Result!=m.Result||p.Installed!=m.Declaration()||p.Builtin!=nil||p.SQL!=nil||p.SQLBody!=nil||p.SQLDefaults!=nil||len(p.Body)!=0 {t.Fatal("new declaration",i,p,err)}
  if p.ACL.Explicit||len(p.ACL.Entries)!=0 {t.Fatal("initial procedure ACL is not absent/default",i,p.ACL)}
  for _,corrupt:=range []func(*catalog.Procedure){
   func(p *catalog.Procedure){p.Signature="25,25"},func(p *catalog.Procedure){p.Result=16},func(p *catalog.Procedure){p.Strict=false},
   func(p *catalog.Procedure){p.Installed.ArgumentNames[0]="source"},func(p *catalog.Procedure){p.Installed.Parallel='u'},func(p *catalog.Procedure){p.Installed.ReturnsSet=true},func(p *catalog.Procedure){p.Installed.Rows=1},func(p *catalog.Procedure){p.Display.Language=catalog.CRoutineLanguage},func(p *catalog.Procedure){p.Implementation=extension.StockCube},
   func(p *catalog.Procedure){p.SQLDefaults=defaults},
  } {
   state:=catalog.CloneState(view.State());bad:=*p;corrupt(&bad);state.SetDescriptor(p.Address(),&bad)
   if err:=extension.StockCitext.ValidateProcedure(view.WithState(state),&bad);err==nil {t.Fatal("corrupt declaration admitted",i)}
  }
 }
 if err:=extension.StockCitext.ValidateExtension(view,installation);err!=nil {t.Fatal(err)}
 metadata,err:=extension.OwnerMetadata(view,e.RoleStore.PublishedRead(&e.Catalog.Owner,view),installation);if err!=nil {t.Fatal(err)}
 record,found,err:=extension.DecodeOwners(strings.TrimSpace(strings.TrimPrefix(metadata,"--")))
 if err!=nil||!found {t.Fatal("owner codec roundtrip",err)}
 if len(record.Members)!=56 {t.Fatal("missing symbolic owners",record)}
 for _,m:=range extension.CitextStringMembers {
  found:=false;for _,owner:=range record.Members {if owner.Name==m.Name&&owner.Signature==m.OwnerSignature() {found=true}}
  if !found {t.Fatal("missing symbolic member signature",m)}
 }
 for _,mutate:=range []func(*extension.OwnerRecord){
  func(r *extension.OwnerRecord){r.Version="1.7"},
  func(r *extension.OwnerRecord){r.Members[55].Signature="citext,citext,citext"},
  func(r *extension.OwnerRecord){r.Members[54].Slot=52},
  func(r *extension.OwnerRecord){r.Members[53].Kind="unrelated"},
 } {
  bad:=record;bad.Members=append([]extension.MemberOwner(nil),record.Members...);mutate(&bad)
  payload,err:=json.Marshal(bad);if err!=nil {t.Fatal(err)}
  if _,_,err:=extension.DecodeOwners(extension.OwnersMarker+" "+base64.StdEncoding.EncodeToString(payload));err==nil {t.Fatal("corrupt symbolic ownership accepted")}
 }
 t.Run("late_conflict",citextStringLateConflict)
}
func TestCitextStringExpression(t *testing.T) {
 e,installation,base:=citextUnitInstalled(t);view:=e.CatalogRead()
 ctx:=&exec.Context{EvalStatement:exec.EvalStatement{Context:context.Background()}}
 for i,m:=range extension.CitextStringMembers {
  call:=citextStringUnitCall(t,view,installation,base,i)
  if call.PreflightFold()!=expr.PreflightRegexNullOrEvaluate||call.StrictNull()!=expr.StrictAfter {t.Fatal("lost strict preflight frontier")}
  got,err:=call.Eval(ctx);if err!=nil {t.Fatal(m.Name,err)}
  output,err:=got.Type().Output(context.Background(),got);want:="XbX";if m.Name=="strpos" {want="1"};if m.Name=="split_part" {want="b"}
  if err!=nil||output!=want {t.Fatal("kernel result",m.Name,output,err)}
  rebuilt,err:=call.Rebuild(call.Info,call.Args);if err!=nil||!call.SameGroup(rebuilt,func(a,b expr.Expr)bool{return a==b},false) {t.Fatal("lost rebuilt capture",m.Name,err)}
  captured,ok,err:=call.Capture(expr.CaptureFunction);if err!=nil||!ok||captured.Count!=len(call.Args) {t.Fatal("capture operands",captured,ok,err)}
  sql,err:=(stored.ScalarRenderer{}).SQL(context.Background(),view,call);if err!=nil||!strings.Contains(sql,m.Name) {t.Fatal("captured rendering",m.Name,sql,err)}
  for _,mutate:=range []func(*functions.CitextStringCapture){
   func(c *functions.CitextStringCapture){c.Member=uint8(52+(i+1)%4)},func(c *functions.CitextStringCapture){c.Ref.Epoch++},func(c *functions.CitextStringCapture){c.Ref=ids.ObjectRef{}},
  } {
   bad:=call;capture:=*call.Citext;mutate(&capture);bad.Citext=&capture
   if _,_,err:=stored.RetagTypes(view,bad,func(t *sqltype.Descriptor)*sqltype.Descriptor{return t});err==nil {t.Fatal("altered capture admitted",m.Name)}
  }
  bad:=call;bad.Textual[0]=false;if err:=bad.ValidateStoredProvider();err==nil {t.Fatal("argument roles substituted")}
  if call.Citext.Member!=uint8(52+i)||call.Args[0].ResultUsage().Type!=base.SQLType {t.Fatal("source changed")}
  other,_,_:=citextUnitInstalled(t)
  if _,_,err:=stored.RetagTypes(other.CatalogRead(),call,func(t *sqltype.Descriptor)*sqltype.Descriptor{return t});err==nil {t.Fatal("foreign domain accepted")}
 }
}
