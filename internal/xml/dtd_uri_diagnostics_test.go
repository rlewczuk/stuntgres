package xml

import (
 "context"
 "errors"
 "strings"
 "testing"
)

func TestXMLDTDDiagnosticsURIOrder(t *testing.T) {
 // xmlErrMsgStr(XML_ERR_INVALID_URI) is counted by PostgreSQL, but unlike
 // xmlFatalErr does not disable declaration callbacks or balanced-chunk parsing.
 // The shared PostgreSQL error collector suppresses a later NOT_WELL_BALANCED,
 // while the invoking entity's own failure remains observable (xml.c handler).
 s:=`<!DOCTYPE a [<!ENTITY bad SYSTEM "bad url"><!ENTITY e "</a>">]><a>&e;</a>`
 at:=len(`<!DOCTYPE a [<!ENTITY bad SYSTEM "bad url"`)
 end:=len(s)-4
 firstSource:=s[:min(80,len(s))]
 parentSource:=s[max(0,end-80):min(len(s),max(0,end-80)+80)]
 want:="line 1: Invalid URI: bad url\n"+firstSource+"\n"+strings.Repeat(" ",at)+"^\nline 1: Entity 'e' failed to parse\n"+parentSource+"\n"+strings.Repeat(" ",min(end,79))+"^"
 for _,collect:=range []bool{false,true}{
  r,d,e:=parse(context.Background(),s,Options{Mode:Document},collect);var pe *ParseError
  if !errors.As(e,&pe)||r.valid||d!=nil||pe.Detail()!=want{t.Fatalf("%+v %v want %s",r,e,want)}
  if len(pe.Diagnostics)!=2||pe.Diagnostics[0].Condition!="XML_ERR_INVALID_URI"||pe.Diagnostics[0].Severity!=Error||pe.Diagnostics[1].Condition!="XML_ERR_UNDECLARED_ENTITY"||pe.Diagnostics[1].Source!=OriginalSource{t.Fatal(pe.Diagnostics)}
 }
}
