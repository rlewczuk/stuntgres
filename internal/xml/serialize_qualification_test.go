package xml

import (
 "context"
 "errors"
 "reflect"
 "testing"
)

func TestXMLSerializeQualificationStructured(t *testing.T){
 // The English/detail/caret are independently SQL-observed; libxml symbolic
 // identity, severity, source and column are matching-source-derived metadata.
 const input=`<a x="1" x="2"/>`
 value,tree,err:=parseExpanded(context.Background(),input,Options{})
 var pe *ParseError
 want:=&ParseError{Category:GrammarError,RequestedMode:Content,EffectiveMode:Content,Diagnostics:[]Diagnostic{{Condition:"XML_ERR_ATTRIBUTE_REDEFINED",Domain:ParserDomain,Severity:Fatal,Line:1,Column:15,Offset:14,Message:"Attribute x redefined",SourceContext:input,Caret:"              ^",Source:OriginalSource}}}
 if value.valid||tree!=nil||!errors.As(err,&pe)||!reflect.DeepEqual(pe,want){t.Fatalf("result %+v tree %v error %+v; want %+v",value,tree,pe,want)}
 const declared=`<!DOCTYPE a [<!ATTLIST a xml:space (default|preserve) "preserve">]><a> <b/> </a>`
 // Default application and blank policy consume the same admitted attribute,
 // rather than reparsing declaration strings or stripping a completed tree.
 const expected="<!DOCTYPE a [\n<!ATTLIST a xml:space (default | preserve) \"preserve\">\n]>\n<a xml:space=\"preserve\"> <b/> </a>"
 requireSerialized(t,serializeValue(t,declared,Content),SerializeOptions{Mode:Document,Indent:true},expected)
 // Both genuine public parse entries feed the same character serializer. No
 // qualification shortcut is inferred from the original requested mode.
 for _,mode:=range []Mode{Content,Document}{v:=serializeValue(t,`<?xml version="1.1" standalone="no"?><a><b/></a>`,mode);requireSerialized(t,v,SerializeOptions{Indent:true},"<a>\n  <b/>\n</a>");requireSerialized(t,v,SerializeOptions{Mode:Document,Indent:true},"<?xml version=\"1.1\" encoding=\"UTF-8\" standalone=\"no\"?>\n<a>\n  <b/>\n</a>")}
}
