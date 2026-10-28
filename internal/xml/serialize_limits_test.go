package xml

import (
 "context"
 "errors"
 "strings"
 "testing"
)

func TestXMLSerializeQualificationCopyVolume(t *testing.T){
 // Source-grounded extension of the existing pinned CORE/DTD copy-volume
 // boundary: actual serialization must emit the admitted ten copies, and its
 // escape/copy loops remain cancelable rather than substituting a parse prefix.
 body:=strings.Repeat("x",1000000)
 prefix:="<!DOCTYPE a [<!ENTITY e \""+body+"\">]>"
 accepted:=prefix+"<a>"+strings.Repeat("&e;",10)+"</a>"
 value:=serializeValue(t,accepted,Content)
 expected:="<!DOCTYPE a [\n<!ENTITY e \""+body+"\">\n]>\n<a>"+strings.Repeat("x",10000000)+"</a>"
 requireSerialized(t,value,SerializeOptions{Mode:Document,Indent:true},expected)
 for _,cause:=range []error{context.Canceled,context.DeadlineExceeded}{
  c:=&phaseContext{Context:context.Background(),phase:"escape",after:4,cause:cause}
  out,err:=Serialize(c,value,SerializeOptions{Indent:true});if out!=""||!errors.Is(err,cause)||!c.admitted{t.Fatal(out,err)}
  requireSerialized(t,serializeValue(t,"<healthy/>",Content),SerializeOptions{Indent:true},"<healthy/>")
 }
 rejected:=prefix+"<a>"+strings.Repeat("&e;",11)+"</a>"
 for _,collect:=range []bool{false,true}{v,tree,err:=parse(context.Background(),rejected,Options{},collect);var pe *ParseError;if v.valid||tree!=nil||!errors.As(err,&pe)||len(pe.Diagnostics)==0||pe.Diagnostics[len(pe.Diagnostics)-1].Condition!="XML_ERR_ENTITY_LOOP"{t.Fatalf("collect %t result %+v tree %v error %v",collect,v,tree,err)}}
 if value.Original()!=accepted{t.Fatal("failed formatting modified retained source")}
}
