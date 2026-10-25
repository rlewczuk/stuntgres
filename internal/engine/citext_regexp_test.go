package engine

import (
 "context"
 "errors"
 "testing"

 "stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/extension"
 "stuntgres/internal/engine/functions"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/plan"
 "stuntgres/internal/engine/stored"
 "stuntgres/internal/sqltype"
)

func citextRegexpUnitCall(t *testing.T,view catalog.Read,installation *catalog.Extension,base *catalog.Type,index int) functions.RegexpCall {
 t.Helper();member:=extension.CitextRegexpMembers[index];p:=view.MustProcedure(installation.Members[42+index]);result,_:=view.LookupType(sqltype.ID(member.Result));text,_:=view.LookupType(sqltype.Text)
 values:=[]string{"AbA","a","i","i"};if member.Name=="regexp_replace" {values[2]="X"}
 args:=make([]expr.Expr,member.ArgCount);var slots [7]uint8
 for i:=range args {typ:=text;if i<2 {typ=base.SQLType};args[i]=plan.LiteralExpr{Info:expr.Info{Usage:typ.Usage()},Value:sqltype.String(typ,values[i])};slots[i]=uint8(i)}
 call,err:=functions.NewCitextRegexpCall(expr.Info{Usage:result.Usage(),RoutineReference:p,Property:expr.Properties{Volatility:expr.Immutable}},member.Name,args,slots,[7]string{},functions.CitextRegexpCapture{Ref:view.Owner().Domain.Ref(view.Epoch(),p.Address()),Namespace:"public",Name:p.Name(),Member:uint8(42+index),Text:text.Usage(),CanInline:!member.Declaration.ReturnsSet&&member.Declaration.ArgumentNames[member.ArgCount-1]!="flags"});if err!=nil{t.Fatal(err)};return call
}
func TestCitextRegexpProvider(t *testing.T) {
 e,installation,base:=citextUnitInstalled(t);view:=e.CatalogRead()
 if len(installation.Members)!=extension.CitextMemberCount||extension.CitextRegexpProcedureEnd!=52||extension.CitextPatternOperatorEnd!=42||len(extension.CitextRegexpMembers)!=10 {t.Fatal("wrong installed inventory")}
 for slot,address:=range installation.Members {
 want:=ids.ProcedureClassID;if slot==0 {want=ids.TypeClassID}else if slot>=9&&slot<15||slot>=26&&slot<42 {want=ids.OperatorClassID}
 if address.ClassID!=want {t.Fatal("wrong member class",slot,address)}
 }
 for i,member:=range extension.CitextRegexpMembers {
 p:=view.MustProcedure(installation.Members[42+i]);symbol,err:=extension.ProcedureSymbol(view,p)
 if err!=nil||symbol!=member.Name||p.Signature!=member.Signature(base.SQLType.ID())||p.Result!=member.Result||p.Installed!=member.Declaration||p.Builtin!=nil||p.SQL!=nil {t.Fatal("wrong declaration",i,p,symbol,err)}
 if core.ProcedureReturnsSet(p)!=member.Declaration.ReturnsSet {t.Fatal("routine admission lost set status",i)}
 }
 if err:=extension.StockCitext.ValidateExtension(view,installation);err!=nil{t.Fatal(err)}
 t.Run("late_conflict",citextRegexpLateConflict)
}
func TestCitextRegexpIdentity(t *testing.T) {
 e,installation,base:=citextUnitInstalled(t);view:=e.CatalogRead()
 for index:=range extension.CitextRegexpMembers {
 call:=citextRegexpUnitCall(t,view,installation,base,index);p:=view.MustProcedure(installation.Members[42+index])
 for _,corrupt:=range []func(*catalog.Procedure){
 func(p *catalog.Procedure){p.Signature="25,25"},func(p *catalog.Procedure){p.Result=16},func(p *catalog.Procedure){p.Strict=false},func(p *catalog.Procedure){p.Implementation=extension.StockCube},func(p *catalog.Procedure){p.Installed.ReturnsSet=!p.Installed.ReturnsSet},func(p *catalog.Procedure){p.Installed.Rows++},func(p *catalog.Procedure){p.Installed.ArgumentNames[0]="source"},func(p *catalog.Procedure){p.Installed.Parallel='u'},func(p *catalog.Procedure){p.Display.Language=catalog.CRoutineLanguage},
 func(p *catalog.Procedure){p.Language=ids.ObjectAddress{ClassID:2612,ObjectID:13}},
 } {
 state:=catalog.CloneState(view.State());bad:=*p;corrupt(&bad);state.SetDescriptor(p.Address(),&bad)
 if err:=extension.StockCitext.ValidateProcedure(view.WithState(state),&bad);err==nil{t.Fatal("corrupt provider admitted",index)}
 node:=expr.Expr(functions.Regexp{RegexpCall:call});if call.Plan.ReturnsSet(){node=functions.RegexpSet{RegexpCall:call}}
 // Set declarations are checked through query deparse; scalar replay forbids sets.
 if !call.Plan.ReturnsSet(){if _,err:=(stored.ScalarRenderer{}).SQL(context.Background(),view.WithState(state),node);err==nil{t.Fatal("substituted declaration refreshed captured authority")}}
 }
 for _,mutate:=range []func(*functions.CitextRegexpCapture){
 func(c *functions.CitextRegexpCapture){c.Member=uint8(42+(index+1)%10)},
 func(c *functions.CitextRegexpCapture){c.Ref.Epoch++},
 func(c *functions.CitextRegexpCapture){c.Ref=ids.ObjectRef{}},
 func(c *functions.CitextRegexpCapture){c.CanInline=!c.CanInline},
 } {
 altered:=call;capture:=*call.Citext;mutate(&capture);altered.Citext=&capture
 node:=expr.Expr(functions.Regexp{RegexpCall:altered});if altered.Plan.ReturnsSet(){node=functions.RegexpSet{RegexpCall:altered}}
 if _,_,err:=stored.RetagTypes(view,node,func(t *sqltype.Descriptor)*sqltype.Descriptor{return t});err==nil{t.Fatal("corrupt captured authority admitted",index)}
 }
 other,_,_:=citextUnitInstalled(t)
 if _,_,err:=stored.RetagTypes(other.CatalogRead(),functions.Regexp{RegexpCall:call},func(t *sqltype.Descriptor)*sqltype.Descriptor{return t});err==nil{t.Fatal("foreign generation admitted")}
 state:=catalog.CloneState(view.State());bad:=*installation;bad.Members=append([]ids.ObjectAddress(nil),installation.Members...);bad.Members[42+index]=installation.Members[41];state.SetDescriptor(bad.Address(),&bad)
 if err:=extension.StockCitext.ValidateExtension(view.WithState(state),&bad);err==nil{t.Fatal("corrupt member admitted")}
 }
}
func TestCitextRegexpExpression(t *testing.T) {
 e,installation,base:=citextUnitInstalled(t);view:=e.CatalogRead();ctx:=context.Background()
 for index,member:=range extension.CitextRegexpMembers {
 call:=citextRegexpUnitCall(t,view,installation,base,index)
 var values [7]sqltype.Value;for i,arg:=range call.Args {values[i]=arg.(plan.LiteralExpr).Value}
 state:=new(sqltype.PGRegexExecution)
 if member.Declaration.ReturnsSet {
 got,err:=call.Plan.EvaluateSet(ctx,values,state);if err!=nil||len(got)==0{t.Fatal("set kernel",got,err)}
 values[1]=sqltype.Null(base.SQLType);got,err=call.Plan.EvaluateSet(ctx,values,state);if err!=nil||len(got)!=0{t.Fatal("strict set NULL",got,err)}
 node:=functions.RegexpSet{RegexpCall:call};rebuilt,err:=node.Rebuild(call.Info,call.Args);if err!=nil||!node.SameGroup(rebuilt,func(a,b expr.Expr)bool{return a==b},false){t.Fatal("set rebuild",err)}
 } else {
 got,err:=call.Plan.Evaluate(ctx,values,state);if err!=nil{t.Fatal(err)}
 expected:="{A}";if member.Name=="regexp_replace"{expected="XbA"};if member.Name=="regexp_split_to_array"{expected=`{"",b,""}`}
 output,err:=got.Type().Output(ctx,got);if err!=nil||output!=expected {t.Fatal("original spelling",output,expected,err)}
 values[1]=sqltype.Null(base.SQLType);got,err=call.Plan.Evaluate(ctx,values,state);if err!=nil||!got.IsNull(){t.Fatal("strict scalar NULL",got,err)}
 node:=functions.Regexp{RegexpCall:call};rebuilt,err:=node.Rebuild(call.Info,call.Args);if err!=nil||!node.SameGroup(rebuilt,func(a,b expr.Expr)bool{return a==b},false){t.Fatal("scalar rebuild",err)}
 if _,err:=(stored.ScalarRenderer{}).SQL(ctx,view,node);err!=nil{t.Fatal("captured replay",err)}
 }
 capture,ok,err:=call.Capture(expr.CaptureFunction);if err!=nil||!ok||capture.Count!=len(call.Args){t.Fatal("capture",capture,ok,err)}
 canceled,cancel:=context.WithCancel(ctx);cancel();values[1]=call.Args[1].(plan.LiteralExpr).Value
 if member.Declaration.ReturnsSet {_,err=call.Plan.EvaluateSet(canceled,values,state)}else{_,err=call.Plan.Evaluate(canceled,values,state)}
 if !errors.Is(err,context.Canceled){t.Fatal("cancellation",err)}
 }
 citextRegexpWrittenOrder(t)
 citextRegexpSQLBodyBoundaries(t)
}
