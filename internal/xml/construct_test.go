package xml

import (
 "context"
 "errors"
 "reflect"
 "strings"
 "testing"
 "unsafe"
)

func TestXMLProduceEscapesNames(t *testing.T){
 ctx:=context.Background();input:="<>&\"'\t\n\r é😀\x01\x08\x0b\x0c\x1f\x7f"
 for _,tc:=range []struct{profile EscapeProfile;want string}{
  {TextEscape,"&lt;&gt;&amp;&quot;'\t\n&#13; é😀\x01\x08\x0b\x0c\x1f\x7f"},
  {ContentEscape,"&lt;&gt;&amp;\"'\t\n&#x0d; é😀\x01\x08\x0b\x0c\x1f\x7f"},
  {AttributeEscape,"&lt;&gt;&amp;&quot;'&#9;&#10;&#13; &#xE9;&#x1F600;\x01\x08\x0b\x0c\x1f\x7f"},
 }{got,err:=Escape(ctx,input,tc.profile);if err!=nil||got!=tc.want{t.Fatalf("profile %d: %q %v",tc.profile,got,err)}}
 for _,tc:=range []struct{input,want string;inferred bool}{
  {"foo$bar","foo_x0024_bar",false},{"_x","_x005F_x",false},{"_x1_","_x005F_x1_",false},{":a:b.c","_x003A_a:b.c",false},{":a:b.c","_x003A_a_x003A_b.c",true},
  {"xmlThing","xmlThing",false},{"xmlThing","_x0078_mlThing",true},{"XMLThing","_x0058_MLThing",true},{"é😀‌","é_x1F600__x200C_",false},{"́a","_x0301_a",false},{"á","á",false},
  {"aͅ","aͅ",false},{"a͆","a_x0346_",false},{"a·","a·",false},{"·a","_x00B7_a",false},{"a〩","a〩",false},{"a〪","a〪",false},
 }{got,err:=MapName(ctx,tc.input,tc.inferred);if err!=nil||got!=tc.want{t.Fatalf("name %q inferred%v: %q %v",tc.input,tc.inferred,got,err)}}
}

func TestXMLProduceGenuineOwnership(t *testing.T){
 ctx:=context.Background()
 original,err:=ConstructText(ctx,"<&\x01");if err!=nil||!original.Valid()||original.Original()!="&lt;&amp;\x01"||original.RequestedMode()!=Content{t.Fatal(original,err)}
 backing:=unsafe.StringData(original.Original());saved:=original
 if _,err:=Parse(ctx,original.Original(),Options{});err==nil{t.Fatal("constructor characters unexpectedly accepted by XML input")}
 output,warning,err:=OutputText(ctx,original);if err!=nil||warning!=(OutputWarning{})||output!=original.Original(){t.Fatal(output,warning,err)}
 for _,text:=range []string{"a--b","a-"}{bad,err:=ConstructComment(ctx,text);var ce *ConstructError;if !errors.As(err,&ce)||bad.Valid()||bad.Original()!=""||ce.Message!="invalid XML comment"{t.Fatal(bad,err)}}
 empty,err:=ConstructElement(ctx,"r",nil,[]string{""});if err!=nil||empty.Original()!=`<r></r>`{t.Fatal(empty,err)}
 absent,err:=ConstructElement(ctx,"r",nil,nil);if err!=nil||absent.Original()!=`<r/>`{t.Fatal(absent,err)}
 data:="   x  ";pi,null,err:=ConstructPI(ctx,"p",&data,true);if err!=nil||null||pi.Original()!=`<?p x  ?>`{t.Fatal(pi,null,err)}
 bad,null,err:=ConstructPI(ctx,"XmL",nil,true);if err==nil||null||bad.Valid(){t.Fatal("target must precede NULL",bad,null,err)}
 bad,null,err=ConstructPI(ctx,"p",nil,true);if err!=nil||!null||bad.Valid(){t.Fatal("explicit NULL",bad,null,err)}
 if saved.Original()!=original.Original()||unsafe.StringData(original.Original())!=backing||!reflect.DeepEqual(saved,original){t.Fatal("later constructor/output changed accepted backing")}
 if (Result{}).Valid(){t.Fatal("zero accepted")}
}

func TestXMLProduceDeclarations(t *testing.T){
 ctx:=context.Background();a:=accept(t,`<?xml version="1.1" standalone="yes"?><a/>`,Content);b:=accept(t,`<?xml version="1.0" standalone="no"?><b/>`,Content)
 merged,err:=ConstructConcat(ctx,[]Result{a,b});if err!=nil||merged.Original()!=`<?xml version="1.0" standalone="no"?><a/><b/>`{t.Fatal(merged,err)}
 for _,tc:=range []struct{version *string;standalone RootStandalone;want string}{
  {nil,RootPreserve,`<?xml version="1.0" standalone="yes"?><a/>`},{nil,RootRemove,`<a/>`},{new(""),RootRemove,`<?xml version=""?><a/>`},{new("2.0"),RootNo,`<?xml version="2.0" standalone="no"?><a/>`},
 }{got,err:=ConstructRoot(ctx,a,tc.version,tc.standalone);if err!=nil||got.Original()!=tc.want{t.Fatal(got,err)}}
 bad,err:=ConstructRoot(ctx,accept(t,`<a/>`,Content),new(`x" y`),RootPreserve);if err!=nil{t.Fatal(err)}
 for _,tc:=range []struct{values []Result;want string}{{[]Result{bad},`<?xml version="x"?>`},{[]Result{bad,b},`<b/>`},{[]Result{b,bad},`<b/>`}}{got,err:=ConstructConcat(ctx,tc.values);if err!=nil||got.Original()!=tc.want{t.Fatal(got,err)}}
 got,err:=ConstructRoot(ctx,bad,nil,RootRemove);if err!=nil||!got.Valid()||got.Original()!=""{t.Fatal(got,err)}
 if got,err:=ConstructConcat(ctx,[]Result{{}});err==nil||got.Valid(){t.Fatal("zero operand accepted")}
}

func TestXMLProduceCancellation(t *testing.T){
 text:=strings.Repeat("é<&",12000)
 for _,tc:=range []struct{phase string;after int;run func(context.Context)(Result,error)}{
  {"Escape",3,func(c context.Context)(Result,error){return ConstructText(c,text)}},
  {"Escape",16,func(c context.Context)(Result,error){return ConstructText(c,text)}},
  {"ConstructComment",3,func(c context.Context)(Result,error){return ConstructComment(c,strings.Repeat("x",20000))}},
  {"constructJoin",3,func(c context.Context)(Result,error){return ConstructElement(c,"r",nil,[]string{text})}},
 }{c:=&phaseContext{Context:context.Background(),phase:tc.phase,after:tc.after,cause:context.Canceled};got,err:=tc.run(c);if !c.admitted||!errors.Is(err,context.Canceled)||got.Valid()||got.Original()!=""{t.Fatal(tc.phase,got,err,c)}}
 c:=&phaseContext{Context:context.Background(),phase:"MapName",after:3,cause:context.Canceled};got,err:=MapName(c,strings.Repeat("é_",12000),false);if !c.admitted||!errors.Is(err,context.Canceled)||got!=""{t.Fatal(got,err,c)}
 empty:=make([]string,1024)
 c=&phaseContext{Context:context.Background(),phase:"constructJoin",after:len(empty)+3,cause:context.Canceled}
 got,err=constructJoin(c,empty)
 if !c.admitted||!errors.Is(err,context.Canceled)||got!=""{t.Fatal("empty-part emission",got,err,c)}
 if appendSize(int(^uint(0)>>1),1){t.Fatal("size overflow admitted")}
 healthy,err:=ConstructText(context.Background(),"healthy");if err!=nil||healthy.Original()!="healthy"{t.Fatal(healthy,err)}
}

func TestXMLProduceNamePresentation(t *testing.T){
 for _,tc:=range []struct{input,want string}{
  {"plain","plain"},{"_x005F_x0041_","_x0041_"},{"_x0078_mlThing","xmlThing"},
  {"a_x003A_b","a:b"},{"_x200C_","\u200c"},{"_x1F600_","_x1F600_"},
  {"_x0041__x0042_","AB"},{"_x00e9__x00E9_","éé"},
  {"_x0041","_x0041"},{"_x00GG_","_x00GG_"},{"_X0041_","_X0041_"},
  // The first seven bytes decode to underscore; literal x005F then precedes
  // the independent seven-byte escape at offset 12, which decodes to A.
  {"prefix_x0041_suffix","prefixAsuffix"},{"_x005F_x005F_x0041_","_x005FA"},
 }{if got:=UnmapName(tc.input);got!=tc.want{t.Fatal(tc.input,got,tc.want)}}
}
