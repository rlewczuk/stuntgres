package xml

import (
 "testing"
)

// Independent pinned PostgreSQL18.6 captures859 precede these literals. English
// messages/context/caret and acceptance are observed; structured coordinates and
// condition identities follow the matching Debian Name/NCName productions.
func TestXMLCoreNameEntryDiagnostics(t *testing.T) {
 for _,eol:=range []struct{name,value string}{{"crlf","\r\n"},{"cr","\r"},{"lf","\n"}} {
  e:=eol.value;h:=0;line:=2;if e=="\r\n" {h=1};if e=="\r" {line=1}
  col:=1;if e=="\r" {col=4}
  checkCurrentReadWitness(t,currentReadWitness{name:"pi_"+eol.name,input:"<?"+e+"?><a/>",events:[]currentReadEvent{{"XML_ERR_PI_NOT_STARTED","xmlParsePI : no target name",1,3,2+h,"<?",2}},documentExtra:&currentReadEvent{"XML_ERR_DOCUMENT_EMPTY","Start tag expected, '<' not found",line,col,2+len(e),"?><a/>",0}})
  checkCurrentReadWitness(t,currentReadWitness{name:"entity_"+eol.name,input:"<a>&"+e+";</a>",events:[]currentReadEvent{{"XML_ERR_NAME_REQUIRED","xmlParseEntityRef: no name",1,5,4+h,"<a>&",4}}})
  checkCurrentReadWitness(t,currentReadWitness{name:"attribute_entity_"+eol.name,input:"<a x=\"&"+e+";\"/>",events:[]currentReadEvent{{"XML_ERR_NAME_REQUIRED","xmlParseEntityRef: no name",1,8,7+h,"<a x=\"&",7}}})
  col=1;if e=="\r" {col=3}
  checkCurrentReadWitness(t,currentReadWitness{name:"element_"+eol.name,input:"<"+e+"/>",events:[]currentReadEvent{{"XML_ERR_NAME_REQUIRED","StartTag: invalid element name",1,2,1+h,"<",1}},documentExtra:&currentReadEvent{"XML_ERR_DOCUMENT_END","Extra content at the end of the document",line,col,1+len(e),"/>",0}})
  for _,input:=range []string{"<p:"+e+"/>","<a p:"+e+"=\"v\"/>","<p:a:"+e+"/>"} {
   checkCurrentReadWitness(t,currentReadWitness{name:"qname_"+eol.name,input:input})
  }
  col=2;if e=="\r" {col=8}
  checkCurrentReadWitness(t,currentReadWitness{name:"end_name_"+eol.name,input:"<a></"+e+">",events:[]currentReadEvent{{"XML_ERR_TAG_NAME_MISMATCH","Opening and ending tag mismatch: a line 1 and unparsable",line,col,6+len(e),">",1}}})
  col=2;if e=="\r" {col=12}
  checkCurrentReadWitness(t,currentReadWitness{name:"end_local_"+eol.name,input:"<p:a></p:"+e+">",events:[]currentReadEvent{{"XML_ERR_TAG_NAME_MISMATCH","Opening and ending tag mismatch: a line 1 and p:",line,col,10+len(e),">",1}}})
 }
 for _,target:=range []string{"1","-","."} {
  input:="<?"+target+"?><a/>"
  checkCurrentReadWitness(t,currentReadWitness{name:"invalid_ascii_"+target,input:input,events:[]currentReadEvent{{"XML_ERR_PI_NOT_STARTED","xmlParsePI : no target name",1,3,2,input,2}},documentExtra:&currentReadEvent{"XML_ERR_DOCUMENT_EMPTY","Start tag expected, '<' not found",1,3,2,input,2}})
 }
 for _,target:=range []string{":","_","a"} {checkCurrentReadWitness(t,currentReadWitness{name:"ascii_start_"+target,input:"<?"+target+"?><a/>"})}
 for _,tc:=range []currentReadWitness{
  {name:"pi_nul",input:"<?\x00?><a/>",binaryOnly:true,events:[]currentReadEvent{{"XML_ERR_PI_NOT_STARTED","xmlParsePI : no target name",1,3,2,"<?",2}},documentExtra:&currentReadEvent{"XML_ERR_DOCUMENT_EMPTY","Start tag expected, '<' not found",1,3,2,"<?",2}},
  {name:"pi_ff",input:"<?\xff?><a/>",binaryOnly:true,events:[]currentReadEvent{{"XML_ERR_INVALID_CHAR","Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x3F 0x3E 0x3C",1,3,2,"<?\xff?><a/>",2}}},
  {name:"entity_nul",input:"<a>&\x00;</a>",binaryOnly:true,events:[]currentReadEvent{
   {"XML_ERR_NAME_REQUIRED","xmlParseEntityRef: no name",1,5,4,"<a>&",4},
   {"XML_ERR_TAG_NOT_FINISHED","Premature end of data in tag a line 1",1,5,4,"<a>&",4},
  }},
  {name:"entity_ff",input:"<a>&\xff;</a>",binaryOnly:true,events:[]currentReadEvent{
   {"XML_ERR_INVALID_CHAR","Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x3B 0x3C 0x2F",1,5,4,"<a>&\xff;</a>",4},
   {"XML_ERR_UNDECLARED_ENTITY","Entity '\xff' not defined",1,7,6,"<a>&\xff;</a>",6},
  }},
  {name:"local_nul",input:"<p:\x00/>",binaryOnly:true,events:[]currentReadEvent{{"XML_ERR_GT_REQUIRED","Couldn't find end of Start Tag p: line 1",1,4,3,"<p:",3}}},
  {name:"local_ff",input:"<p:\xff/>",binaryOnly:true,events:[]currentReadEvent{
   {"XML_ERR_NAME_REQUIRED","error parsing attribute name",1,4,3,"<p:\xff/>",3},
   {"XML_ERR_SPACE_REQUIRED","attributes construct error",1,4,3,"<p:\xff/>",3},
   {"XML_ERR_GT_REQUIRED","Couldn't find end of Start Tag p: line 1",1,4,3,"<p:\xff/>",3},
  },documentExtra:&currentReadEvent{"XML_ERR_DOCUMENT_END","Extra content at the end of the document",1,4,3,"<p:\xff/>",3},contentExtra:&currentReadEvent{"XML_ERR_INTERNAL_ERROR","internal error: detected an error in element content",1,4,3,"<p:\xff/>",3}},
 } {checkCurrentReadWitness(t,tc)}
}
