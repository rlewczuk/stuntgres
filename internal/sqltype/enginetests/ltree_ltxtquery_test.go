package enginetests

import (
 "context"
 "errors"
 "strings"
 "testing"
 "stuntgres/internal/sqltype"
)

func TestLtreeLtxtqueryEvaluation(t *testing.T) {
 d:=ltreeArrayUnitTypes(t)
 for _,tc:=range []struct{path,query string;want bool}{
  {"Europe.Russia","Europe & Russia",true},{"Russia.x.Europe","Europe & Russia",true},{"x.Europe.y.Russia.z","Europe & Russia",true},{"Europe.x","Europe & Russia",false},
  {"Top.Blocked","Top | Europe & !Blocked",true},{"Top.Blocked","(Top | Europe) & !Blocked",false},{"Europe","!Top & Europe",true},{"Top","!Top & Europe",false},{"Top","!(Top & Europe)",true},{"Other","!!Top",false},{"Top","!!Top",true},{"Top.Other","!Top",false},
  {"","foo",false},{"","!foo",true},{"Foo","foo",false},{"Foo","foo@",true},{"food","foo",false},{"food","foo*",true},{"fo","foo*",false},
  {"bar_foo","foo_bar%",true},{"foo","foo_foo%",true},{"foo.bar","foo_bar%",false},{"foo-bar","foo_bar%",false},{"foo_barbaz","foo_bar%",false},{"foo1_bar2_baz","foo_bar%*",true},{"foo1_br2_baz","foo_bar%*",false},{"FOO1_BAR2_baz","foo_bar%@*",true},
 } {
  path,query:=ltreeArrayInput(t,d.path,tc.path),ltreeArrayInput(t,d.text,tc.query)
  savedPath,savedQuery:=path,query
  for op:=sqltype.LtreeLtxtqueryForward;op<sqltype.LtreeLtxtqueryOperationCount;op++ {
   a,b,at,bt:=path,query,d.path,d.text;if op==sqltype.LtreeLtxtqueryReverse{a,b,at,bt=b,a,bt,at}
   for range 3 {
    value,err:=op.Eval(t.Context(),a,b,at,bt,d.boolean);if err!=nil{t.Fatal(err)}
    got,err:=value.Boolean();if err!=nil||got!=tc.want||value.Type()!=d.boolean{t.Fatal(tc,got,err)}
   }
   for _,args:=range [][2]sqltype.Value{{sqltype.Null(at),b},{a,sqltype.Null(bt)},{sqltype.Null(at),sqltype.Null(bt)}} {
    value,err:=op.Eval(t.Context(),args[0],args[1],at,bt,d.boolean);if err!=nil||!value.IsNull()||value.Type()!=d.boolean{t.Fatal("typed NULL",value,err)}
   }
  }
  if path!=savedPath||query!=savedQuery{t.Fatal("immutable operands changed")}
 }
}

func TestLtreeLtxtqueryValidation(t *testing.T) {
 d,foreign:=ltreeArrayUnitTypes(t),ltreeArrayUnitTypes(t)
 // Pure descriptor factories accept arbitrary logical IDs. VoidValue supplies
 // an exact-descriptor non-NULL carrier without either private family payload,
 // avoiding unsafe layout mutation or a new production inspection seam.
 missingPath,err:=sqltype.Ltree(sqltype.Void,"missing_path","public.missing_path");if err!=nil{t.Fatal(err)}
 missingQuery,err:=sqltype.Ltxtquery(sqltype.Void,"missing_query","public.missing_query");if err!=nil{t.Fatal(err)}
 for op:=sqltype.LtreeLtxtqueryForward;op<sqltype.LtreeLtxtqueryOperationCount;op++ {
  at,bt:=d.path,d.text;ft,gt:=foreign.path,foreign.text
  if op==sqltype.LtreeLtxtqueryReverse{at,bt,ft,gt=bt,at,gt,ft}
  for _,types:=range [][3]*sqltype.Descriptor{{nil,bt,d.boolean},{at,nil,d.boolean},{at,bt,nil},{bt,at,d.boolean},{at,at,d.boolean},{d.query,bt,d.boolean},{at,bt,d.path}} {
   if err:=op.Validate(types[0],types[1],types[2]);err==nil{t.Fatal("invalid formal signature admitted")}
  }
  a,b:=ltreeArrayInput(t,at,"a"),ltreeArrayInput(t,bt,"a")
  for _,args:=range [][2]sqltype.Value{{ltreeArrayInput(t,ft,"a"),b},{sqltype.Null(ft),sqltype.Null(bt)},{a,ltreeArrayInput(t,gt,"a")},{sqltype.Null(at),sqltype.Null(gt)}} {
   if _,err:=op.Eval(t.Context(),args[0],args[1],at,bt,d.boolean);err==nil{t.Fatal("foreign or invalid carrier admitted")}
  }
  for _,pair:=range [][2]sqltype.Value{
   {sqltype.VoidValue(missingPath),ltreeArrayInput(t,d.text,"a")},
   {sqltype.VoidValue(missingPath),sqltype.Null(d.text)},
   {ltreeArrayInput(t,d.path,"a"),sqltype.VoidValue(missingQuery)},
   {sqltype.Null(d.path),sqltype.VoidValue(missingQuery)},
  }{
   left,right:=pair[0],pair[1];if op==sqltype.LtreeLtxtqueryReverse{left,right=right,left}
   value,err:=op.Eval(t.Context(),left,right,left.Type(),right.Type(),d.boolean)
   if err==nil||err.Error()!="invalid ltree ltxtquery payload"||value.Type()!=nil{t.Fatal("missing carrier was not rejected before NULL propagation",value,err)}
  }
 }
 if err:=sqltype.LtreeLtxtqueryOperationCount.Validate(d.path,d.text,d.boolean);err==nil{t.Fatal("invalid operation admitted")}
}

func TestLtreeLtxtqueryCancellation(t *testing.T) {
 d:=ltreeArrayUnitTypes(t);path:=ltreeArrayInput(t,d.path,strings.Repeat("a.",4095)+"z");query:=ltreeArrayInput(t,d.text,"z & !absent")
 for op:=sqltype.LtreeLtxtqueryForward;op<sqltype.LtreeLtxtqueryOperationCount;op++ {
  a,b,at,bt:=path,query,d.path,d.text;if op==sqltype.LtreeLtxtqueryReverse{a,b,at,bt=b,a,bt,at}
  ctx,cancel:=context.WithCancel(t.Context());cancel()
  if _,err:=op.Eval(ctx,a,b,at,bt,d.boolean);!errors.Is(err,context.Canceled){t.Fatal("pre-cancellation",err)}
  for _,remaining:=range []int{2,3,5}{ctx:=&ltreeWorkContext{Context:t.Context(),remaining:remaining};if _,err:=op.Eval(ctx,a,b,at,bt,d.boolean);!errors.Is(err,context.Canceled){t.Fatal("in-work cancellation",remaining,err)}}
 }
}
