package enginetests

import (
 "context"
 "errors"
 "strings"
 "testing"
 "stuntgres/internal/sqltype"
)

func TestLtreeLqueryOperation(t *testing.T) {
 d:=ltreeArrayUnitTypes(t)
 for _,tc:=range []struct{path,query string;want bool}{
  {"Top.A.Child","Top.*{1,2}.Child",true},{"Top.A.B.Child","Top.*{1,2}.Child",true},{"Top.Child","Top.*{1,2}.Child",false},{"Top.A.B.C.Child","Top.*{1,2}.Child",false},{"Other.A.Child","Top.*{1,2}.Child",false},{"Top.A.Child.Tail","Top.*{1,2}.Child",false},{"top.A.Child","Top.*{1,2}.Child",false},
  {"","*",true},{"","a{0}",true},{"","a",false},{"a.b","a",false},{"a.a","a{0,2}.a",true},{"a.a.b","a{1,2}.b",true},{"a.b","a{2}.b",false},{"a.b","*.*.b.*{0}",true},{"a","a{0}.a",true},
  {"b","a|b",true},{"c","a|b",false},{"a.c","a.!b|d",true},{"a.b","a.!b|d",false},{"c.c","!a|b{2}",true},{"c.b","!a|b{2}",false},
  {"Foo","foo",false},{"Foo","foo@",true},{"food","foo*",true},{"fo","foo*",false},{"food","foo",false},{"bar_foo","foo_bar%",true},{"foo","foo_foo%",true},{"a","_%",true},{"_bar__foo_","__foo__bar_%",true},{"foo-bar","foo_bar%",false},{"foo1_bar2_baz","foo_bar%*",true},{"foo1_br2_baz","foo_bar%*",false},{"FOO1_BAR2_baz","foo_bar%@*",true},
 } {
  path,query:=ltreeArrayInput(t,d.path,tc.path),ltreeArrayInput(t,d.query,tc.query)
  saved:=[2]sqltype.Value{path,query}
  for op:=sqltype.LtreeLqueryMatch;op<sqltype.LtreeLqueryOperationCount;op++ {
   a,b:=path,query;if op==sqltype.LtreeLqueryReverseMatch{a,b=b,a}
   value,err:=op.Eval(t.Context(),a,b,a.Type(),b.Type(),d.boolean)
   if err!=nil{t.Fatal(tc,op,err)};got,err:=value.Boolean();if err!=nil||got!=tc.want||value.Type()!=d.boolean{t.Fatal(tc,op,got,err)}
   for _,args:=range [][2]sqltype.Value{{sqltype.Null(a.Type()),b},{a,sqltype.Null(b.Type())},{sqltype.Null(a.Type()),sqltype.Null(b.Type())}} {
    value,err:=op.Eval(t.Context(),args[0],args[1],a.Type(),b.Type(),d.boolean);if err!=nil||!value.IsNull()||value.Type()!=d.boolean{t.Fatal("typed NULL",value,err)}
   }
  }
  if saved!=[2]sqltype.Value{path,query}{t.Fatal("immutable operands changed")}
 }
}

func TestLtreeLqueryValidation(t *testing.T) {
 d,foreign:=ltreeArrayUnitTypes(t),ltreeArrayUnitTypes(t)
 for op:=sqltype.LtreeLqueryMatch;op<sqltype.LtreeLqueryOperationCount;op++ {
  a,b:=ltreeArrayInput(t,d.path,"a"),ltreeArrayInput(t,d.query,"a")
  fa,fb:=ltreeArrayInput(t,foreign.path,"a"),ltreeArrayInput(t,foreign.query,"a")
  if op==sqltype.LtreeLqueryReverseMatch{a,b=b,a;fa,fb=fb,fa}
  for _,args:=range [][2]sqltype.Value{{fa,b},{a,fb},{sqltype.Null(fa.Type()),sqltype.Null(b.Type())},{sqltype.Null(a.Type()),sqltype.Null(fb.Type())},{sqltype.Value{},b},{a,sqltype.Value{}}} {
   value,err:=op.Eval(t.Context(),args[0],args[1],a.Type(),b.Type(),d.boolean);if err==nil||value.Type()!=nil{t.Fatal("foreign or invalid operand admitted",op)}
  }
  for _,formals:=range [][3]*sqltype.Descriptor{{nil,b.Type(),d.boolean},{a.Type(),nil,d.boolean},{b.Type(),a.Type(),d.boolean},{a.Type(),b.Type(),d.path},{a.Type(),b.Type(),nil}} {
   if err:=op.Validate(formals[0],formals[1],formals[2]);err==nil{t.Fatal("invalid formal signature admitted",op)}
  }
 }
 if err:=sqltype.LtreeLqueryOperationCount.Validate(d.path,d.query,d.boolean);err==nil{t.Fatal("unknown role admitted")}
}

func TestLtreeLqueryCancellation(t *testing.T) {
 d:=ltreeArrayUnitTypes(t)
 for _,tc:=range []struct{path,query string;want bool}{{strings.Repeat("a.",127)+"a","*.*.a{1,128}",true},{strings.Repeat("a_",127)+"a","z%",false}} {
  a,b:=ltreeArrayInput(t,d.path,tc.path),ltreeArrayInput(t,d.query,tc.query)
  for op:=sqltype.LtreeLqueryMatch;op<sqltype.LtreeLqueryOperationCount;op++ {
   left,right:=a,b;if op==sqltype.LtreeLqueryReverseMatch{left,right=right,left}
   canceled,cancel:=context.WithCancel(t.Context());cancel()
   contexts:=[]context.Context{canceled,&ltreeWorkContext{Context:t.Context(),remaining:2},&ltreeWorkContext{Context:t.Context(),remaining:8}}
   for _,ctx:=range contexts {
    value,err:=op.Eval(ctx,left,right,left.Type(),right.Type(),d.boolean)
    var state interface{SQLState()string}
    if !errors.Is(err,context.Canceled)||errors.As(err,&state)||value.Type()!=nil{t.Fatal("ordinary cancellation without partial result",value,err)}
   }
   value,err:=op.Eval(t.Context(),left,right,left.Type(),right.Type(),d.boolean);if err!=nil{t.Fatal("canceled scratch leaked",err)};got,err:=value.Boolean();if err!=nil||got!=tc.want{t.Fatal("successful retry",got,err)}
  }
  type outcome struct{value sqltype.Value;err error}
  results:=make(chan outcome,4);start:=make(chan struct{})
  for range 4{go func(){<-start;v,err:=sqltype.LtreeLqueryMatch.Eval(t.Context(),a,b,d.path,d.query,d.boolean);results<-outcome{v,err}}()}
  close(start)
  for range 4{result:=<-results;if result.err!=nil{t.Fatal(result.err)};got,err:=result.value.Boolean();if err!=nil||got!=tc.want{t.Fatal("shared immutable input",got,err)}}
 }
}
