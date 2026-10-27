package xml

import (
 "strings"
 "testing"
)

func TestXMLCoreCDATAReadNormalization(t *testing.T) {
 // Five independent815/825 witnesses; exact records/source-derived structured
 // coordinates are mapped in local://xml-core-pi-crlf-evidence.md. Twenty
 // primary mode/route combinations preserve CDATA's distinct stale recovery.
 const limit="CData section too big found"
 const stray="Sequence ']]>' not allowed in content"
 const internal="internal error: detected an error in element content"
 for _,eol:=range []struct{name,value string}{{"crlf","\r\n"},{"cr","\r"},{"lf","\n"}} {
  q:=10000015;h:=0;if eol.value=="\r\n" {h=1}
  tc:=currentReadWitness{name:"overlength_"+eol.name,input:"<a><![CDATA["+strings.Repeat("x",10000003)+eol.value+"]]></a>",events:[]currentReadEvent{
   {"XML_ERR_CDATA_NOT_FINISHED",limit,1,q+1,q+h,strings.Repeat("x",80),79},
  }}
  if eol.value=="\r" {
   tc.events=append(tc.events,currentReadEvent{"XML_ERR_MISPLACED_CDATA_END",stray,1,q+2,q+1,"]]></a>",0})
  }else{
   tc.events=append(tc.events,
    currentReadEvent{"XML_ERR_MISPLACED_CDATA_END",stray,2,1,q+h,strings.Repeat("x",80),79},
    currentReadEvent{"XML_ERR_INTERNAL_ERROR",internal,2,3,q+len(eol.value)+1,"]]></a>",1},
   )
  }
  checkCurrentReadWitness(t,tc)
 }
 checkCurrentReadWitness(t,currentReadWitness{name:"below_committed_limit",input:"<a><![CDATA["+strings.Repeat("x",9999999)+"\r\n]]></a>"})
 checkCurrentReadWitness(t,currentReadWitness{name:"exact_lookahead_boundary",input:"<a><![CDATA["+strings.Repeat("x",10000000)+"\r\n]]></a>",events:[]currentReadEvent{
  {"XML_ERR_CDATA_NOT_FINISHED",limit,2,3,10000016,"]]></a>",2},
  {"XML_ERR_INTERNAL_ERROR",internal,2,4,10000016,"]]></a>",2},
 }})
}
