package xml

import (
 "context"
 "errors"
 "testing"
)

// These are pinned parser.c branch witnesses, not claimed oracle captures.
func dtdAttributeTypeDiagnostics(t *testing.T) {
 t.Helper()
 for _,tc:=range []struct{input,typ string;ok bool;offset int;condition Condition;message string}{
  {"IDREFSsuffix","IDREFS",true,6,"",""},
  {"NOTATION (n)","NOTATION",true,12,"",""},
  {"(1|1)","ENUMERATION",true,5,"",""},
  {"BOGUS","",false,0,"XML_ERR_ATTLIST_NOT_STARTED","'(' required to start ATTLIST enumeration"},
  {"NOTATION(n)","",false,8,"XML_ERR_SPACE_REQUIRED","Space required after 'NOTATION'"},
  {"NOTATION n","",false,9,"XML_ERR_NOTATION_NOT_STARTED","NOTATION: Name expected here"},
  {"NOTATION ()","",false,10,"XML_ERR_NAME_REQUIRED","Name expected in NOTATION declaration"},
  {"NOTATION (n|)","",false,12,"XML_ERR_NAME_REQUIRED","Name expected in NOTATION declaration"},
  {"NOTATION (n","",false,11,"XML_ERR_NOTATION_NOT_FINISHED","'>' required to close NOTATION declaration"},
  {"()","",false,1,"XML_ERR_NMTOKEN_REQUIRED","NmToken expected in ATTLIST enumeration"},
  {"(n|)","ENUMERATION",true,3,"XML_ERR_NMTOKEN_REQUIRED","NmToken expected in ATTLIST enumeration"},
  {"(n","ENUMERATION",true,2,"XML_ERR_ATTLIST_NOT_FINISHED","')' required to finish ATTLIST enumeration"},
 }{t.Run("type "+tc.input,func(t *testing.T){
  p:=parser{ctx:context.Background(),s:tc.input,line:1,column:1,inDTD:true}
  typ,ok:=p.attributeType()
  if p.err!=nil||typ!=tc.typ||ok!=tc.ok||p.i!=tc.offset{t.Fatalf("type %q/%t at %d err %v, want %q/%t at %d",typ,ok,p.i,p.err,tc.typ,tc.ok,tc.offset)}
  if tc.condition==""{if len(p.events)!=0{t.Fatalf("unexpected diagnostics: %+v",p.events)};return}
  if len(p.events)!=1{t.Fatalf("diagnostics %+v",p.events)}
  d:=p.events[0];if d.Condition!=tc.condition||d.Message!=tc.message||d.Offset!=tc.offset||d.Severity!=Fatal||d.Domain!=ParserDomain{t.Fatalf("diagnostic %+v",d)}
 })}
 // A failed type at '>' still consumes that declaration delimiter. Neither
 // ATTLIST nor its caller invents a missing-close diagnostic in this case.
 for _,tc:=range []struct{declaration string;conditions []Condition}{
  {"<!ATTLIST a x >",[]Condition{"XML_ERR_ATTLIST_NOT_STARTED"}},
  {"<!ATTLIST a x (n>",[]Condition{"XML_ERR_ATTLIST_NOT_FINISHED","XML_ERR_SPACE_REQUIRED"}},
  {"<!ATTLIST a x NOTATION (n>",[]Condition{"XML_ERR_NOTATION_NOT_FINISHED"}},
 }{for _,collect:=range []bool{false,true}{
  input:="<!DOCTYPE a ["+tc.declaration+"]><a/>"
  r,tree,err:=parse(context.Background(),input,Options{Mode:Document},collect)
  var pe *ParseError;if !errors.As(err,&pe)||r.valid||tree!=nil{t.Fatalf("%q: result %+v tree %+v err %v",input,r,tree,err)}
  if len(pe.Diagnostics)!=len(tc.conditions){t.Fatalf("%q: diagnostics %+v",input,pe.Diagnostics)}
  for i,want:=range tc.conditions{if pe.Diagnostics[i].Condition!=want{t.Fatalf("%q: diagnostic %d %+v want %s",input,i,pe.Diagnostics[i],want)}}
 }}
}
