package sqltype

import (
 "context"
 "errors"
 "testing"
 "stuntgres/internal/sqlstate"
)

func testTypes() map[ID]*Descriptor { m:=map[ID]*Descriptor{};for _,d:=range Builtins(){m[d.ID()]=d};return m }
func TestTypeCoreIdentityAndNull(t *testing.T) {
 types:=testTypes();for _,tc:=range []struct{id ID;name,sql,driver string}{{16,"bool","BOOLEAN","BOOL"},{20,"int8","BIGINT","INT8"},{23,"int4","INTEGER","INT4"},{25,"text","TEXT","TEXT"},{705,"unknown","",""}}{d:=types[tc.id];if d.Name()!=tc.name||d.SQLName()!=tc.sql||d.DatabaseName()!=tc.driver{t.Fatal(d)};if d.Usage().Typmod!=-1{t.Fatal(d.Usage())}}
 if (Value{}).Type()!=nil || (Value{}).IsNull() || types[Unknown].Concrete(){t.Fatal("invalid/unknown became concrete")}
 for _,id:=range []ID{Int4,Int8,Text,Bool}{v:=Null(types[id]);if v.Type()!=types[id]||!v.IsNull(){t.Fatal(v)};literal,err:=v.Type().SQLLiteral(context.Background(),v);if err!=nil||literal!="NULL"{t.Fatal(literal,err)}}
 for _,d:=range []*Descriptor{nil,types[Unknown]}{func(){defer func(){if recover()==nil{t.Error("invalid NULL accepted")}}();Null(d)}()}
 v:=String(types[Text],"owned");copy:=v;if s,err:=copy.Text();err!=nil||s!="owned"{t.Fatal(s,err)};if _,err:=v.Integer();err==nil{t.Fatal("wrong accessor accepted")}
}
func TestTypeCoreIntegerInput(t *testing.T) {
 types:=testTypes();ctx:=context.Background()
 for _,id:=range []ID{Int4,Int8}{d:=types[id];for _,tc:=range []struct{text string;want int64}{{" +0007 ",7},{"\t\n\r\f\v-8\t",-8},{"0x_FF",255},{"0o17",15},{"0b10_10",10},{"1_000",1000},{"08",8},{"-0X_10",-16}}{v,err:=d.Input(ctx,tc.text,-1);if err!=nil{t.Fatal(tc,err)};n,err:=v.Integer();if err!=nil||n!=tc.want{t.Fatal(tc,n,err)};out,err:=d.Output(ctx,v);if err!=nil||out==""{t.Fatal(out,err)}}
  name:="integer";bounds:=[]string{"2147483648","-2147483649"};if id==Int8{name="bigint";bounds=[]string{"9223372036854775808","-9223372036854775809"}}
  for _,text:=range []string{"_1","1_","1__0","0b2","1.2","\u00a01",bounds[0]+"x"}{_,err:=d.Input(ctx,text,-1);want:=sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"22P02",Message:"invalid input syntax for type "+name+": \""+text+"\""};var got *sqlstate.Error;if !errors.As(err,&got)||*got!=want{t.Fatalf("%q got %#v want %#v",text,got,want)}}
  for _,text:=range append(bounds,"99999999999999999999999999999x"){_,err:=d.Input(ctx,text,-1);want:=sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"22003",Message:"value \""+text+"\" is out of range for type "+name};var got *sqlstate.Error;if !errors.As(err,&got)||*got!=want{t.Fatalf("%q got %#v want %#v",text,got,want)}}
 }
 for _,tc:=range []struct{id ID;text string;want int64}{{Int4,"-2147483648",-2147483648},{Int4,"2147483647",2147483647},{Int8,"-9223372036854775808",-9223372036854775808},{Int8,"9223372036854775807",9223372036854775807}}{v,err:=types[tc.id].Input(ctx,tc.text,-1);if err!=nil{t.Fatal(err)};n,_:=v.Integer();if n!=tc.want{t.Fatal(n)}}
}
func TestTypeCoreBooleanAndCoercion(t *testing.T) {
 types:=testTypes();ctx:=context.Background();d:=types[Bool]
 for _,tc:=range []struct{text string;want bool}{{"t",true},{"TR",true},{" yes ",true},{"ON",true},{"1",true},{"f",false},{"NO",false},{"of",false},{"0",false}}{v,err:=d.Input(ctx,tc.text,-1);if err!=nil{t.Fatal(err)};b,_:=v.Boolean();if b!=tc.want{t.Fatal(tc,b)};out,_:=d.Output(ctx,v);literal,_:=d.SQLLiteral(ctx,v);wantOut,wantLiteral:="f","FALSE";if b{wantOut,wantLiteral="t","TRUE"};if out!=wantOut||literal!=wantLiteral{t.Fatal(out,literal)};plan,err:=PlanCoercion(d.Usage(),types[Text].Usage(),Assignment);if err!=nil{t.Fatal(err)};text,err:=plan.Apply(ctx,v);if err!=nil{t.Fatal(err)};s,_:=text.Text();want:="false";if b{want="true"};if s!=want{t.Fatal(s)}}
 for _,s:=range []string{"o","","truth"}{_,err:=d.Input(ctx,s,-1);var got *sqlstate.Error;if !errors.As(err,&got)||got.Code!="22P02"||got.Message!="invalid input syntax for type boolean: \""+s+"\""{t.Fatal(err)}}
 for _,pair:=range [][2]ID{{Bool,Int4},{Int4,Bool},{Bool,Text},{Int8,Int4}}{if _,err:=PlanCoercion(types[pair[0]].Usage(),types[pair[1]].Usage(),Implicit);err==nil{t.Fatal("implicit conversion accepted",pair)}}
 wide,err:=PlanCoercion(types[Int4].Usage(),types[Int8].Usage(),Implicit);if err!=nil{t.Fatal(err)};v,_:=Integer(types[Int4],2147483647);converted,err:=wide.Apply(ctx,v);if err!=nil||converted.Type()!=types[Int8]{t.Fatal(converted,err)}
 narrow,err:=PlanCoercion(types[Int8].Usage(),types[Int4].Usage(),Assignment);if err!=nil{t.Fatal(err)};v,_=Integer(types[Int8],2147483648);_,err=narrow.Apply(ctx,v);var got *sqlstate.Error;if !errors.As(err,&got)||got.Code!="22003"||got.Message!="integer out of range"{t.Fatal(err)}
 null,err:=wide.Apply(ctx,Null(types[Int4]));if err!=nil||!null.IsNull()||null.Type()!=types[Int8]{t.Fatal(null,err)}
 other:=testTypes();foreign,_:=Integer(other[Int4],1);if _,err:=wide.Apply(ctx,foreign);err==nil{t.Fatal("foreign value accepted")}
 cmp,err:=BindComparison(types[Int4],types[Int8]);if err!=nil{t.Fatal(err)};a,_:=Integer(types[Int4],2147483647);b,_:=Integer(types[Int8],9223372036854775807);if n,err:=cmp.Compare(ctx,a,b);err!=nil||n!=-1{t.Fatal(n,err)}
}

func TestTypeCoreStrictEqualityAndSerialization(t *testing.T) {
 types:=testTypes()
 ctx:=context.Background()
 equality,err:=BindEquality(types[Int4],types[Int8],types[Bool])
 if err!=nil{t.Fatal(err)}
 a,_:=Integer(types[Int4],7)
 b,_:=Integer(types[Int8],7)
 result,err:=equality.Evaluate(ctx,a,b)
 if err!=nil||result.Type()!=types[Bool]||result.IsNull(){t.Fatal(result,err)}
 equal,err:=result.Boolean()
 if err!=nil||!equal{t.Fatal(equal,err)}
 result,err=equality.Evaluate(ctx,a,Null(types[Int8]))
 if err!=nil||result.Type()!=types[Bool]||!result.IsNull(){t.Fatal(result,err)}
 if _,err=BindEquality(types[Int4],types[Bool],types[Bool]);err==nil{t.Fatal("incompatible equality admitted")}
 text:=String(types[Text],"O'Brien\n\\tail")
 literal,err:=types[Text].SQLLiteral(ctx,text)
 if err!=nil||literal!="'O''Brien\n\\tail'"{t.Fatal(literal,err)}
 if _,err=types[Text].Output(ctx,Value{});err==nil{t.Fatal("zero output accepted")}
 if _,err=types[Unknown].Input(ctx,"x",-1);err==nil{t.Fatal("unknown published by input")}
 if _,err=types[Int4].Input(ctx,"7",0);err==nil{t.Fatal("unsupported modifier accepted")}
}
