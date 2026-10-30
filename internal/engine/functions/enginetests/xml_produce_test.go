package enginetests_test

import (
 "context"
 "errors"
 "reflect"
 "testing"
 "stuntgres/internal/engine"
 "stuntgres/internal/engine/analyze"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/functions"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqltype"
)

func TestXMLProduceExpressionProtocols(t *testing.T){
 e:=engine.New();b:=enginetest.ExpressionBinder(t,context.Background(),e,sqltype.InputEnvironment{},nil)
 for _,q:=range []string{`SELECT xmlconcat('<a/>','<b/>')`,`SELECT xmlelement(NAME "r$",XMLATTRIBUTES(1 AS "a$b"),'x')`,`SELECT xmlelement(NAME r,xmlcomment('owned'))`,`SELECT xmlforest(1 AS a,2 AS b)`,`SELECT xmlpi(NAME p,'x')`,`SELECT xmlroot('<a/>',VERSION '1.1',STANDALONE NO)`}{
  raw:=enginetest.Statement(t,q).(*parser.Select).Targets[0].(parser.XMLConstructExpr);node,err:=b.Bind(raw,analyze.ExpressionPolicy{Context:analyze.ProjectionContext});if err!=nil{t.Fatal(q,err)};recipe,ok:=node.(functions.XMLConstruct);if !ok{t.Fatalf("wrong recipe %T",node)}
  children:=recipe.Children();saved:=append([]expr.Expr(nil),children...)
  rebuilt,err:=recipe.Rebuild(expr.InfoOf(recipe),children);if err!=nil||!reflect.DeepEqual(recipe,rebuilt)||!recipe.SameGroup(rebuilt,func(a,b expr.Expr)bool{return reflect.DeepEqual(a,b)},true){t.Fatal("rebuild",q,err)}
  capture,ok,err:=recipe.Capture(expr.CaptureXMLConstruct);if err!=nil||!ok||capture.Check(recipe,len(raw.Children()))!=nil{t.Fatal("capture",q,capture,err)}
  rendering,err:=recipe.Render();if err!=nil||len(rendering.Args)!=len(children){t.Fatal("render",q,err)}
  if _,err:=recipe.Rebuild(expr.InfoOf(recipe),nil);err==nil{t.Fatal("missing children admitted")}
  if len(children)>0{children[0]=nil};if !reflect.DeepEqual(recipe.Children(),saved){t.Fatal("child projection lent mutable recipe backing")}
  names:=recipe.Names();if len(names)>0{names[0]="forged"};if reflect.DeepEqual(recipe.Names(),names)&&len(names)>0{t.Fatal("name projection lent mutable recipe backing")}
  forged:=raw;forged.Standalone=3;if raw.Kind!=parser.XMLRoot{if _,err:=functions.NewXMLConstruct(context.Background(),expr.InfoOf(recipe),forged,recipe.Name(),recipe.Names(),saved);err==nil{t.Fatal("foreign option admitted")}}
  if raw.Kind==parser.XMLElement||raw.Kind==parser.XMLPI{if _,err:=functions.NewXMLConstruct(context.Background(),expr.InfoOf(recipe),raw,"forged",recipe.Names(),saved);err==nil{t.Fatal("foreign name admitted")}}
  if len(raw.Args)>0{if call,ok:=raw.Args[0].(parser.FunctionExpr);ok{
   annotated:=raw;annotated.Args=append([]parser.Expr(nil),raw.Args...)
   call.StoredProviderRoutine,call.StoredProviderSymbol=true,"xmlcomment";annotated.Args[0]=call
   if !recipe.MatchesSyntax(annotated){t.Fatal("ordinary descendant annotation changed parent occurrence")}
   annotated.Name.Text="forged"
   if recipe.MatchesSyntax(annotated){t.Fatal("parent name forgery acquired descendant authority")}
  }}
 }
 for _,tc:=range []struct{q string;oid uint32}{ {`SELECT xmltext('x')`,3813},{`SELECT xmlcomment('x')`,2895} }{raw:=enginetest.Statement(t,tc.q).(*parser.Select).Targets[0];node,err:=b.Bind(raw,analyze.ExpressionPolicy{Context:analyze.ProjectionContext});if err!=nil{t.Fatal(err)};call,ok:=node.(functions.XMLTextCall);if !ok||call.CallRoutineReference()==nil||call.CallRoutineReference().RoutineOID()!=tc.oid||call.PlanningFold()!=expr.FoldConstant||call.PreflightFold()!=expr.PreflightNullOrEvaluate{t.Fatal("ordinary folding/identity",node)}}
}

func TestXMLProduceEvaluationOrder(t *testing.T){
 e:=engine.New();text:=enginetest.Usage(e,sqltype.Text);xmlUsage:=enginetest.Usage(e,sqltype.XML);date:=enginetest.Usage(e,sqltype.Date)
 order:=[]int{};cause:=errors.New("later SQL evaluation")
 child:=func(usage sqltype.Usage,value sqltype.Value,n int,failure error)expr.Expr{return ivUnitChild{Info:expr.Info{Usage:usage},value:value,order:&order,ordinal:n,failure:failure}}
 infinite,err:=date.Type.Input(context.Background(),"infinity",-1);if err!=nil{t.Fatal(err)}
 raw:=parser.XMLConstructExpr{Kind:parser.XMLElement,Name:parser.Identifier{Text:"r"},Named:[]parser.XMLNamedOperand{{Expr:parser.LiteralExpr{Value:parser.Value{Kind:parser.NullValue}},Name:new(parser.Identifier{Text:"a"})}},Args:[]parser.Expr{parser.LiteralExpr{Value:parser.Value{Kind:parser.NullValue}}}}
 recipe,err:=functions.NewXMLConstruct(context.Background(),expr.Info{Usage:xmlUsage},raw,"r",[]string{"a"},[]expr.Expr{child(date,infinite,1,nil),child(text,sqltype.Null(text.Type),2,cause)});if err!=nil{t.Fatal(err)}
 if _,err:=recipe.Eval(newIVUnitContext());!errors.Is(err,cause)||!reflect.DeepEqual(order,[]int{1,2}){t.Fatal("mapping preceded later child Eval",order,err)}
 order=nil
 piRaw:=parser.XMLConstructExpr{Kind:parser.XMLPI,Name:parser.Identifier{Text:"XML"},Args:raw.Args}
 pi,err:=functions.NewXMLConstruct(context.Background(),expr.Info{Usage:xmlUsage},piRaw,"XML",nil,[]expr.Expr{child(text,sqltype.Null(text.Type),1,cause)});if err!=nil{t.Fatal(err)}
 if _,err:=pi.Eval(newIVUnitContext());!errors.Is(err,cause)||!reflect.DeepEqual(order,[]int{1}){t.Fatal("target preceded child Eval",order,err)}
}
