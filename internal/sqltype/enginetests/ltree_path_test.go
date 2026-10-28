package enginetests

import (
 "context"
 "errors"
 "strings"
 "testing"
 "stuntgres/internal/sqltype"
)

type ltreePathUnitCase struct { op sqltype.LtreePathOperation; formals [8]*sqltype.Descriptor; values [8]sqltype.Value; result *sqltype.Descriptor; want string }
func ltreePathUnitCases(t *testing.T) []ltreePathUnitCase {
 t.Helper(); d := ltreeArrayUnitTypes(t)
 var integer,text *sqltype.Descriptor
 for _,typ := range sqltype.Builtins() { if typ.ID()==sqltype.Int4 { integer=typ }; if typ.ID()==sqltype.Text { text=typ } }
 var cases []ltreePathUnitCase
 for op := sqltype.LtreePathOperation(0); op < sqltype.LtreePathOperationCount; op++ {
  tc := ltreePathUnitCase{op:op,result:d.path,want:"A.B"}
  for i := range op.Arity() { tc.formals[i]=d.path;tc.values[i]=ltreeArrayInput(t,d.path,"A.B.C") }
  num := func(i int,n int64) { tc.formals[i]=integer;var err error;tc.values[i],err=sqltype.Integer(integer,n);if err!=nil{t.Fatal(err)} }
  txt := func(i int,s string) { tc.formals[i]=text;tc.values[i]=sqltype.String(text,s) }
  switch op {
  case sqltype.LtreeSubltree: num(1,0);num(2,2)
  case sqltype.LtreeSubpathLength: num(1,0);num(2,2)
  case sqltype.LtreeSubpath: num(1,1);tc.want="B.C"
  case sqltype.LtreeIndex: tc.result=integer;tc.want="0"
  case sqltype.LtreeIndexOffset: num(2,1);tc.result=integer;tc.want="-1"
  case sqltype.LtreeNlevel: tc.result=integer;tc.want="3"
  case sqltype.LtreeToText: tc.result=text;tc.want="A.B.C"
  case sqltype.LtreeFromText: txt(0,"A.B")
  case sqltype.LtreeLCAArray:tc.formals[0]=d.paths;tc.values[0]=ltreeArrayInput(t,d.paths,"[7:8]={A.B.C,A.B.D}")
  case sqltype.LtreeConcat:tc.want="A.B.C.A.B.C"
  case sqltype.LtreeConcatText:txt(1,"D");tc.want="A.B.C.D"
  case sqltype.LtreeTextConcat:txt(0,"D");tc.want="D.A.B.C"
  }
  cases=append(cases,tc)
 }
 return cases
}
func TestLtreePathKernels(t *testing.T) {
 for _,tc := range ltreePathUnitCases(t) {
  saved:=tc.values
  if err:=tc.op.Validate(tc.formals,tc.op.Arity(),tc.result);err!=nil{t.Fatal(tc.op,err)}
  for range 3 {
   value,err:=tc.op.Eval(t.Context(),tc.values,tc.formals,tc.op.Arity(),tc.result);if err!=nil{t.Fatal(tc.op,err)}
   text,err:=tc.result.Output(t.Context(),value);if err!=nil||text!=tc.want{t.Fatal(tc.op,text,tc.want,err)}
  }
  if tc.values!=saved{t.Fatal("mutated input backing")}
  for i:=range tc.op.Arity(){values:=tc.values;values[i]=sqltype.Null(tc.formals[i]);value,err:=tc.op.Eval(t.Context(),values,tc.formals,tc.op.Arity(),tc.result);if err!=nil||!value.IsNull()||value.Type()!=tc.result{t.Fatal("typed NULL",tc.op,i,err)}}
 }
 d:=ltreeArrayUnitTypes(t);empty:=ltreeArrayInput(t,d.path,"");path:=ltreeArrayInput(t,d.path,"A.B.C")
 for _,values:=range [][8]sqltype.Value{{empty,path},{path,empty}}{
  value,err:=sqltype.LtreeConcat.Eval(t.Context(),values,[8]*sqltype.Descriptor{d.path,d.path},2,d.path)
  if err!=nil||value!=path{t.Fatal("empty concatenation failed immutable identity",err)}
 }
 for _,tc:=range []struct{a,b string;null bool;want string}{{"A","A",false,""},{"A.B","X.Y",false,""},{"","A",true,""}}{
  values:=[8]sqltype.Value{ltreeArrayInput(t,d.path,tc.a),ltreeArrayInput(t,d.path,tc.b)}
  value,err:=sqltype.LtreeLCA2.Eval(t.Context(),values,[8]*sqltype.Descriptor{d.path,d.path},2,d.path)
  if err!=nil||value.IsNull()!=tc.null{t.Fatal("empty proper ancestor framing",err)}
  if !tc.null{out,err:=d.path.Output(t.Context(),value);if err!=nil||out!=tc.want{t.Fatal("empty proper ancestor output",out,err)}}
 }
 for _,tc:=range ltreePathUnitCases(t){
  if tc.op!=sqltype.LtreeSubltree{continue}
  values:=tc.values;values[1],_=sqltype.Integer(tc.formals[1],-1)
  value,err:=tc.op.Eval(t.Context(),values,tc.formals,3,tc.result)
  var state interface{SQLState()string}
  if value.Type()!=nil||!errors.As(err,&state)||state.SQLState()!="22023"{t.Fatal("negative bound accepted",err)}
  values=tc.values;values[0]=ltreeArrayInput(t,tc.formals[0],"")
  value,err=tc.op.Eval(t.Context(),values,tc.formals,3,tc.result)
  if value.Type()!=nil||!errors.As(err,&state)||state.SQLState()!="22023"{t.Fatal("empty slice has no valid position",err)}
 }
 for _,tc:=range ltreePathUnitCases(t){
  if tc.op!=sqltype.LtreeIndexOffset{continue}
  tc.values[1]=ltreeArrayInput(t,tc.formals[1],"A")
  for _,boundary:=range []struct{offset,want int64}{{-99,0},{-2147483648,-1},{2147483647,-1}}{
   values:=tc.values;values[2],_=sqltype.Integer(tc.formals[2],boundary.offset)
   value,err:=tc.op.Eval(t.Context(),values,tc.formals,3,tc.result)
   if err!=nil{t.Fatal("index offset boundary",boundary.offset,err)}
   got,err:=value.Integer();if err!=nil||got!=boundary.want{t.Fatal("released index int32 arithmetic",boundary.offset,got,boundary.want,err)}
  }
 }
}
func TestLtreePathIdentity(t *testing.T) {
 for _,tc:=range ltreePathUnitCases(t) {
  for i:=range tc.op.Arity(){
   foreign:=*tc.formals[i]
   for _,null:=range []bool{false,true}{
    values:=tc.values
    if null{values[i]=sqltype.Null(&foreign)}else if foreign.IsLtree(){values[i]=ltreeArrayInput(t,&foreign,"A.B")}else{continue}
    if _,err:=tc.op.Eval(t.Context(),values,tc.formals,tc.op.Arity(),tc.result);err==nil{t.Fatal("foreign generation admitted",tc.op,i)}
   }
   bad:=tc.formals;bad[i]=nil;if tc.op.Validate(bad,tc.op.Arity(),tc.result)==nil{t.Fatal("missing formal admitted")}
  }
  if tc.op.Validate(tc.formals,tc.op.Arity()-1,tc.result)==nil{t.Fatal("wrong count admitted")}
  if tc.op.Validate(tc.formals,tc.op.Arity(),nil)==nil{t.Fatal("missing result admitted")}
  values:=tc.values;values[0]=sqltype.Value{};if _,err:=tc.op.Eval(t.Context(),values,tc.formals,tc.op.Arity(),tc.result);err==nil{t.Fatal("invalid carrier admitted")}
  if tc.result.IsLtree(){foreign:=*tc.result;if tc.op!=sqltype.LtreeFromText&&tc.op.Validate(tc.formals,tc.op.Arity(),&foreign)==nil{t.Fatal("foreign result admitted")}}
 }
}
func TestLtreePathCancellation(t *testing.T) {
 for _,tc:=range ltreePathUnitCases(t){ctx,cancel:=context.WithCancel(t.Context());cancel();v,err:=tc.op.Eval(ctx,tc.values,tc.formals,tc.op.Arity(),tc.result);if !errors.Is(err,context.Canceled)||v.Type()!=nil{t.Fatal("pre-canceled kernel",err)}}
 d:=ltreeArrayUnitTypes(t);large:=ltreeArrayInput(t,d.path,strings.Repeat("a.",4095)+"a")
 for _,op:=range []sqltype.LtreePathOperation{sqltype.LtreeIndex,sqltype.LtreeLCA2,sqltype.LtreeConcat,sqltype.LtreeSubpath}{
  values:=[8]sqltype.Value{large,large};formals:=[8]*sqltype.Descriptor{d.path,d.path};result:=d.path
  if op==sqltype.LtreeIndex{values[1]=ltreeArrayInput(t,d.path,"b");for _,typ:=range sqltype.Builtins(){if typ.ID()==sqltype.Int4{result=typ}}}
  if op==sqltype.LtreeSubpath{for _,typ:=range sqltype.Builtins(){if typ.ID()==sqltype.Int4{formals[1]=typ;values[1],_=sqltype.Integer(typ,1)}}}
  base,stop:=context.WithCancel(t.Context());ctx:=&ltreeCancelContext{Context:base,cancel:stop,at:10}
  v,err:=op.Eval(ctx,values,formals,2,result);stop()
  if !errors.Is(err,context.Canceled)||v.Type()!=nil||ctx.calls!=10{t.Fatal("interrupted kernel returned result",op,ctx.calls,err)}
 }
}
