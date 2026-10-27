package enginetests

import (
 "context"
 "errors"
 "testing"
 "unsafe"
 "stuntgres/internal/sqltype"
)

func TestHstoreUpdateTyped(t *testing.T) {
 ctx:=context.Background();h:=hstoreUnitType(t,65000);text,array,_:=hstoreConstructBuiltins()
 input:=func(d *sqltype.Descriptor,s string)sqltype.Value{t.Helper();v,err:=d.Input(ctx,s,-1);if err!=nil{t.Fatal(err)};return v}
 v:=input(h,"a=>NULL,b=>2")
 type kernel func(context.Context,sqltype.Value,*sqltype.Descriptor,sqltype.Value)(sqltype.Value,error)
 concat:=func(c context.Context,v sqltype.Value,d *sqltype.Descriptor,r sqltype.Value)(sqltype.Value,error){return v.HstoreConcat(c,d,r)}
 scalar:=func(c context.Context,v sqltype.Value,d *sqltype.Descriptor,r sqltype.Value)(sqltype.Value,error){return v.HstoreDelete(c,d,r)}
 arr:=func(c context.Context,v sqltype.Value,d *sqltype.Descriptor,r sqltype.Value)(sqltype.Value,error){return v.HstoreDeleteArray(c,d,r)}
 pair:=func(c context.Context,v sqltype.Value,d *sqltype.Descriptor,r sqltype.Value)(sqltype.Value,error){return v.HstoreDeleteHstore(c,d,r)}
 slice:=func(c context.Context,v sqltype.Value,d *sqltype.Descriptor,r sqltype.Value)(sqltype.Value,error){return v.HstoreSlice(c,d,r)}
 for _,tc:=range []struct{name string;f kernel;right sqltype.Value;want string}{
 {"concat",concat,input(h,"a=>3,c=>4"),`"a"=>"3", "b"=>"2", "c"=>"4"`},
 {"scalar",scalar,sqltype.String(text,"a"),`"b"=>"2"`},
 {"array",arr,input(array,"[0:1][-1:1]={{b,a,a},{NULL,missing,b}}"),""},
 {"pair",pair,input(h,"a=>NULL,b=>3"),`"b"=>"2"`},
 {"slice",slice,input(array,"{a,missing,NULL,a}"),`"a"=>NULL`},
 } {t.Run(tc.name,func(t *testing.T){
 got,err:=tc.f(ctx,v,h,tc.right);if err!=nil{t.Fatal(err)}
 actual,err:=h.Output(ctx,got);if err!=nil||actual!=tc.want||got.Type()!=h||got.IsNull(){t.Fatal("typed result",actual,err)}
 for _,args:=range [][2]sqltype.Value{{sqltype.Null(h),tc.right},{v,sqltype.Null(tc.right.Type())}} {
 got,err:=tc.f(ctx,args[0],h,args[1]);if err!=nil||!got.IsNull()||got.Type()!=h{t.Fatal("typed whole NULL",err)}
 }
 foreign:=hstoreUnitType(t,h.ID())
 if _,err:=tc.f(ctx,v,foreign,tc.right);err==nil{t.Fatal("foreign source accepted")}
 if tc.right.Type()==h {if _,err:=tc.f(ctx,v,h,input(foreign,""));err==nil{t.Fatal("foreign right accepted")}}
 if _,err:=tc.f(ctx,v,h,sqltype.Value{});err==nil{t.Fatal("invalid right accepted")}
 canceled,cancel:=context.WithCancel(ctx);cancel()
 if _,err:=tc.f(canceled,v,h,tc.right);!errors.Is(err,context.Canceled){t.Fatal("canceled update",err)}
 // Retained immutable strings must be shared, not roundtripped through text.
 original,err:=v.HstoreArray(ctx,h,array,false);if err!=nil{t.Fatal(err)}
 projected,err:=got.HstoreArray(ctx,h,array,false);if err!=nil{t.Fatal(err)}
 for i:=0;i<projected.ArrayLen();i+=2 {
 key,_:=projected.ArrayElement(i).Text()
 if tc.name=="concat"&&key!="b"{continue}
 for j:=0;j<original.ArrayLen();j+=2 {
 oldKey,_:=original.ArrayElement(j).Text();if key!=oldKey{continue}
 if unsafe.StringData(key)!=unsafe.StringData(oldKey){t.Fatal("retained key copied")}
 if projected.ArrayElement(i+1).IsNull()||original.ArrayElement(j+1).IsNull(){continue}
 value,_:=projected.ArrayElement(i+1).Text();oldValue,_:=original.ArrayElement(j+1).Text()
 if value==oldValue&&unsafe.StringData(value)!=unsafe.StringData(oldValue){t.Fatal("retained value copied")}
 }
 }
 before,err:=h.Output(ctx,v);if err!=nil||before!=`"a"=>NULL, "b"=>"2"`{t.Fatal("source mutated",before,err)}
 })}
 for _,tc:=range []struct{f kernel;want string}{{arr,`"a"=>NULL, "b"=>"2"`},{slice,""}} {
 for _,keys:=range []string{"{}","{NULL,NULL}","{missing}"}{
 got,err:=tc.f(ctx,v,h,input(array,keys));if err!=nil{t.Fatal(err)}
 actual,err:=h.Output(ctx,got)
 if err!=nil||got.IsNull()||actual!=tc.want{t.Fatal("empty request result",actual,err)}
 }
 }
}
