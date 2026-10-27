package xml

import (
 "context"
 "strings"
 "unicode/utf8"
)

// readContentDeclaration also serves xml_recv's partial-metadata preflight:
// an encoding parsed before a later error must remain available to its caller.
func readContentDeclaration(ctx context.Context,s string) (Declaration,string,error) {
 d:=Declaration{}
 if !strings.HasPrefix(s,"<?xml") { return d,"",ctx.Err() }
 if len(s)>5 { r,_:=utf8.DecodeRuneInString(s[5:]);if nameChar(r) { return d,"",ctx.Err() } }
 d.Present=true
 p:=parser{ctx:ctx,s:s,i:5,line:1,column:6}
 fail:=func(detail string)(Declaration,string,error){return d,detail,p.err}
 if !p.spaces() { return fail("Space required.") }
 // ASCII validity is checked only after successful syntax, as in xml.c.
 // Preflight must retain encoding parsed before a later syntax/ASCII error.
 nonascii:=false
 quoted:=func() (string,bool) {
  if p.byte()!='\''&&p.byte()!='"' { return "",false }
  q:=p.byte();p.advance(1);start:=p.i
  for p.i<len(s)&&p.byte()!=0&&p.byte()!=q&&p.poll() {
   if p.byte()>=128 {nonascii=true};p.advance(1)
  }
  if p.i==len(s)||p.byte()==0||p.err!=nil { return "",false }
  v:=s[start:p.i];p.advance(1);return v,true
 }
 field:=func(name string) bool { if !p.has(name) { return false };p.advance(len(name));p.spaces();if p.byte()!='=' {return false};p.advance(1);p.spaces();return true }
 if !field("version") { return fail("Malformed declaration: missing version.") }
 v,ok:=quoted();if !ok {return fail("Malformed declaration: missing version.")};d.Version=v
 separated:=p.spaces()
 if p.has("encoding") {
  if !separated {return fail("Space required.")}
  if !field("encoding") {return fail("Missing encoding in text declaration.")}
  v,ok=quoted();if !ok {return fail("Missing encoding in text declaration.")}
  d.Encoding,d.EncodingPresent=v,true;separated=p.spaces()
 }
 if p.has("standalone") {
  if !separated {return fail("Space required.")}
  if !field("standalone") {return fail("standalone accepts only 'yes' or 'no'.")}
  v,ok=quoted();if !ok||v!="yes"&&v!="no" {return fail("standalone accepts only 'yes' or 'no'.")}
  if v=="yes" {d.Standalone=Yes}else{d.Standalone=No};p.spaces()
 }
 if !p.has("?>") {return fail("Parsing XML declaration: '?>' expected.")}
 p.advance(2)
 if nonascii {return fail("Invalid character value.")}
 d.End=p.i
 return d,"",p.ctx.Err()
}

func (p *parser) declEvent(code Condition,msg string) { p.event(code,msg) }
func (p *parser) docStringStart() byte {
 p.spaces()
 if p.byte()!='=' {p.declEvent("XML_ERR_EQUAL_REQUIRED","expected '='");return 0}
 p.advance(1);p.spaces()
 q:=p.byte();if q!='\''&&q!='"' {p.declEvent("XML_ERR_STRING_NOT_STARTED","String not started expecting ' or \"");return 0}
 p.advance(1);return q
}
func (p *parser) documentDeclaration() {
 if !p.has("<?xml")||len(p.s)-p.i<=5||!space(p.s[p.i+5]) {return}
 p.declaration.Present=true;p.advance(5);p.spaces()
 version:=""
 if p.has("version") {
  p.advance(7);q:=p.docStringStart()
  if q!=0 {
   start:=p.i;valid:=false
   // Matching VersionNum consumes one digit, '.', then zero or more digits.
   if p.byte()>='0'&&p.byte()<='9' {
    p.advance(1)
    if p.byte()=='.' {p.advance(1);valid=true;for p.byte()>='0'&&p.byte()<='9'&&p.poll(){p.advance(1)}}
   }
   if valid {version=p.s[start:p.i]}
   if p.byte()!=q {p.declEvent("XML_ERR_STRING_NOT_CLOSED","String not closed expecting \" or '")}else{p.advance(1)}
  }
 }
 p.declaration.Version=version
 if version=="" {p.declEvent("XML_ERR_VERSION_MISSING","Malformed declaration expecting version")}else if version!="1.0" {
  message:=p.joinMessage("Unsupported version '",version,"'")
  if strings.HasPrefix(version,"1.") {p.eventLevel("XML_WAR_UNKNOWN_VERSION",message,Warning)}else{p.declEvent("XML_ERR_UNKNOWN_VERSION",message)}
 }
 if !space(p.byte()) {
  if p.has("?>") {p.advance(2);p.declaration.End=p.i;return}
  p.declEvent("XML_ERR_SPACE_REQUIRED","Blank needed here")
 }
 p.spaces()
 inputEncodingAssigned:=false
 if p.has("encoding") {
  p.advance(8);q:=p.docStringStart()
  if q!=0 {
   start:=p.i;valid:=asciiLetter(p.byte())
   if !valid {p.declEvent("XML_ERR_ENCODING_NAME","Invalid XML encoding name")}else{
    for p.i<len(p.s)&&encChar(p.byte())&&p.poll(){p.advance(1)}
   }
   if p.byte()!=q {p.declEvent("XML_ERR_STRING_NOT_CLOSED","String not closed expecting \" or '")}else{
    label:=p.s[start:p.i];p.advance(1)
    p.declaration.Encoding,p.declaration.EncodingPresent=label,true
    if valid&&!recognizedEncoding(label) {
     p.declEvent("XML_ERR_UNSUPPORTED_ENCODING",p.joinMessage("Unsupported encoding ",label))
     p.stopped=true;return // XML REC requires stopping on unsupported encoding.
    }
    // The special UTF8/UTF16 spellings set ctxt->encoding, not the
    // input encoding checked by xmlParseXMLDecl's separator production.
    if valid {
     inputEncodingAssigned=!((len(label)==4&&(strings.EqualFold(label,"UTF8")))||
      (len(label)==5&&(strings.EqualFold(label,"UTF-8")||strings.EqualFold(label,"UTF16")))||
      (len(label)==6&&strings.EqualFold(label,"UTF-16")))
    }
   }
  }
 }
 if inputEncodingAssigned&&!space(p.byte()) {
  if p.has("?>") {p.advance(2);p.declaration.End=p.i;return}
  p.declEvent("XML_ERR_SPACE_REQUIRED","Blank needed here")
 }
 p.spaces()
 if p.has("standalone") {
  p.advance(10);q:=p.docStringStart()
  if q!=0 {
   if p.has("yes") {p.advance(3);p.declaration.Standalone=Yes}else if p.has("no") {p.advance(2);p.declaration.Standalone=No}else{
    p.declEvent("XML_ERR_STANDALONE_VALUE","standalone accepts only 'yes' or 'no'")
   }
   if p.byte()!=q {p.declEvent("XML_ERR_STRING_NOT_CLOSED","String not closed expecting \" or '")}else{p.advance(1)}
  }
 }
 p.spaces();p.finishDocumentDeclaration()
}

func (p *parser) finishDocumentDeclaration() {
 if p.has("?>") {p.advance(2);p.declaration.End=p.i;return}
 p.declEvent("XML_ERR_XMLDECL_NOT_FINISHED","parsing XML declaration: '?>' expected")
 // MOVETO_ENDTAG recovery belongs only to this declaration production.
 // It must not become a generic scan-to-EOF grammar fallback.
 for p.i<len(p.s)&&p.byte()!=0&&p.byte()!='>'&&p.poll(){p.advance(1)}
 if p.byte()=='>' {p.advance(1);p.declaration.End=p.i}else if p.i<len(p.s) {p.stopped=true}
 // Physical exhaustion still reaches DOCUMENT's required-root diagnostic.
 // An embedded NUL remains a distinct halted-production boundary.
}
func asciiLetter(b byte) bool {return b>='A'&&b<='Z'||b>='a'&&b<='z'}
func encChar(b byte) bool {return asciiLetter(b)||b>='0'&&b<='9'||b=='.'||b=='_'||b=='-'}
