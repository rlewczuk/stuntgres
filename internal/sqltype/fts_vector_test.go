package sqltype

import (
 "context"
 "errors"
 "testing"
)
func TestFTSVectorFunctionsTypedAuthority(t *testing.T){
 types,foreign:=testTypes(),testTypes();ctx:=context.Background()
 for _,tc:=range []struct{op FTSVectorFunction;want string}{
  {FTSVectorLength,"2"},{FTSSetWeight,"'a':1A 'b':2A"},{FTSSetSelectedWeight,"'a':1A 'b':2C"},{FTSStrip,"'a' 'b'"},{FTSDelete,"'b':2C"},{FTSDeleteArray,"'b':2C"},{FTSFilter,"'a':1B"},{FTSVectorToArray,"{a,b}"},
 }{
  var args [3]Usage;var values [3]Value
  for i:=range tc.op.Arity(){id:=tc.op.Formal(i);args[i]=types[id].Usage();input:="a:1B b:2C";switch id{case Char:input="A";case Text:input="a";case 1009:input="{a,NULL,missing}";case 1002:input="{B}"};v,err:=types[id].Input(ctx,input,-1);if err!=nil{t.Fatal(err)};values[i]=v}
  p,err:=BindFTSVector(tc.op,types[tc.op.ResultID()].Usage(),args);if err!=nil{t.Fatal(tc.op,err)}
  v,err:=p.Evaluate(ctx,values);if err!=nil{t.Fatal(tc.op,err)};text,err:=v.Type().Output(ctx,v);if err!=nil||text!=tc.want{t.Fatal(tc.op,text,tc.want,err)}
  if tc.op==FTSVectorToArray&&(v.ArrayRank()!=1||v.ArrayDimension(0)!=(ArrayDimension{Lower:1,Length:2})||v.ArrayElement(0).Type()!=types[Text]){t.Fatal("array authority/shape")}
  for i:=range tc.op.Arity(){
   nulls:=values;nulls[i]=Null(args[i].Type);v,err:=p.Evaluate(ctx,nulls);if err!=nil||!v.IsNull()||v.Type()!=p.result{t.Fatal("strict NULL",err)}
   bad:=values;bad[i]=Null(foreign[tc.op.Formal(i)]);if _,err:=p.Evaluate(ctx,bad);err==nil{t.Fatal("foreign typed NULL")}
   usages:=args;usages[i]=foreign[tc.op.Formal(i)].Usage();if _,err:=BindFTSVector(tc.op,types[tc.op.ResultID()].Usage(),usages);err==nil{t.Fatal("foreign operand authority")}
  }
  if _,err:=BindFTSVector(tc.op,foreign[tc.op.ResultID()].Usage(),args);err==nil{t.Fatal("foreign result authority")}
  bad:=values;bad[0]=Value{typ:types[TSVector]};if _,err:=p.Evaluate(ctx,bad);err==nil{t.Fatal("invalid payload")}
  canceled,cancel:=context.WithCancel(ctx);cancel();if _,err:=p.Evaluate(canceled,values);!errors.Is(err,context.Canceled){t.Fatal(err)}
 }
 if _,err:=BindFTSVector(FTSVectorFunction(255),Usage{},[3]Usage{});err==nil{t.Fatal("invalid operation")}
 for _,op:=range []FTSVectorFunction{FTSDeleteArray,FTSSetSelectedWeight,FTSFilter,FTSVectorToArray}{
  source:=*types[TSVector]
  array:=*types[1009]
  if op==FTSFilter{array=*types[1002];array.element=foreign[Char];source.ftsCharArray=&array}else{array.element=foreign[Text];source.ftsTextArray=&array}
  var args [3]Usage;args[0]=source.Usage()
  if op.Arity()==2{args[1]=array.Usage()}
  if op==FTSSetSelectedWeight{args[1]=types[Char].Usage();args[2]=array.Usage()}
  result:=source.Usage();if op==FTSVectorToArray{result=array.Usage()}
  if _,err:=BindFTSVector(op,result,args);err==nil{t.Fatal("foreign array element authority")}
 }
}
func TestFTSVectorFunctionsSelectorCollationAuthority(t *testing.T){
 types,foreign:=testTypes(),testTypes();ctx:=context.Background()
 registered,err:=RegisteredCollation(88101,950);if err!=nil{t.Fatal(err)}
 for _,op:=range []FTSVectorFunction{FTSDeleteArray,FTSSetSelectedWeight}{
  var args [3]Usage;var values [3]Value
  for i:=range op.Arity(){
   id:=op.Formal(i);args[i]=types[id].Usage()
   input:="cat:1B rat:2C";if id==Char{input="A"};if id==1009{input="{cat,NULL,cat,missing}"}
   values[i],err=types[id].Input(ctx,input,-1);if err!=nil{t.Fatal(err)}
  }
  selector:=op.Arity()-1
  for _,collation:=range []CollationRef{{ID:950,Derivation:Default},{ID:951,Derivation:Default},registered}{
   usages:=args;usages[selector].Collation=collation
   p,err:=BindFTSVector(op,types[TSVector].Usage(),usages);if err!=nil{t.Fatal(op,collation,err)}
   out,err:=p.Evaluate(ctx,values);if err!=nil{t.Fatal(err)}
   text,err:=out.Type().Output(ctx,out);want:="'rat':2C";if op==FTSSetSelectedWeight{want="'cat':1A 'rat':2C"}
   if err!=nil||text!=want{t.Fatal(op,text,want,err)}
   nulls:=values;nulls[selector]=Null(types[1009])
   out,err=p.Evaluate(ctx,nulls);if err!=nil||!out.IsNull()||out.Type()!=types[TSVector]{t.Fatal("collated strict NULL",err)}
   usages[selector].Type=foreign[1009]
   if _,err:=BindFTSVector(op,types[TSVector].Usage(),usages);err==nil{t.Fatal("collation hid foreign generation")}
  }
  for _,bad:=range []Usage{
   {Type:types[1009],Typmod:0,Collation:CollationRef{ID:950,Derivation:Default}},
   {Type:types[1009],Typmod:-1},
   {Type:types[1009],Typmod:-1,Collation:CollationRef{ID:88101,Derivation:Default}},
  }{
   usages:=args;usages[selector]=bad
   if _,err:=BindFTSVector(op,types[TSVector].Usage(),usages);err==nil{t.Fatal("invalid selector usage",bad)}
  }
 }
}
func TestFTSVectorFunctionsCharArrays(t *testing.T){
 types:=testTypes();ctx:=context.Background();d:=types[1002]
 v,err:=d.Input(ctx,`[0:1][4:5]={{AB,b},{NULL,D}}`,-1);if err!=nil{t.Fatal(err)}
 if v.ArrayLen()!=4||v.ArrayRank()!=2||!v.ArrayElement(2).IsNull()||v.ArrayElement(0).text!="A"{t.Fatal("char input/shape")}
 text,err:=d.Output(ctx,v);if err!=nil||text!=`[0:1][4:5]={{A,b},{NULL,D}}`{t.Fatal(text,err)}
 if _,err:=Array(90000,"_boundary",types[Char]);err!=nil{t.Fatal(err)}
 if StoredScalarCastType(Char){t.Fatal("scalar annotation widened")}
}
