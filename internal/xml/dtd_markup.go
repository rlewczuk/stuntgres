package xml

import (
 "fmt"
 "strings"
 "unicode/utf8"
)

func (p *parser) popDTDInput()bool {
 if p.i!=len(p.s)||len(p.peFrames)==0{return false}
 last:=len(p.peFrames)-1;f:=p.peFrames[last];p.peFrames=p.peFrames[:last];p.restoreFrame(f)
 return true
}
func (p *parser) localDelimiter(delimiter string)bool {
 for at:=p.i;at<len(p.s); {
  p.err=p.ctx.Err();if p.err!=nil{return false}
  end:=at+min(4096,len(p.s)-at)
  // The overlap is bounded by the actual lexical delimiter, not a copied
  // replacement input. Normal productions remain the common path.
  if strings.Contains(p.s[at:min(len(p.s),end+len(delimiter)-1)],delimiter){return true};at=end
 }
 return false
}
func (p *parser) dtdComment() {
 if len(p.peFrames)==0||p.localDelimiter("-->"){p.comment();return}
 if p.err!=nil{return}
 id:=p.inputID;p.advance(4);size:=0
 var preview [50]byte;used:=0
 for p.err==nil {
  if p.popDTDInput(){continue}
  if p.has("-->") {
   if id!=p.inputID{p.event("XML_ERR_ENTITY_BOUNDARY","comment doesn't start and stop in the same entity")}
   p.advance(3);return
  }
  if p.has("--") {
   message:="Double hyphen within comment";if size>0{message+=": <!--"+strings.TrimRight(string(preview[:used]),"\n")}
   p.event("XML_ERR_HYPHEN_IN_COMMENT",message)
  }
  r,n:=p.currentNormalizedCharacter()
  if n==0 {p.event("XML_ERR_COMMENT_NOT_FINISHED","Comment not terminated");return}
  if !xmlChar(r){p.event("XML_ERR_INVALID_CHAR",fmt.Sprintf("xmlParseComment: invalid xmlChar value %d",r));return}
  p.advanceCharacter(r,n);size+=n
  if used<len(preview){var encoded [4]byte;width:=utf8.EncodeRune(encoded[:],r);used+=copy(preview[used:],encoded[:width])}
  if size>maxText{p.event("XML_ERR_COMMENT_NOT_FINISHED","Comment too big found");return}
 }
}
func (p *parser) dtdPI() {
 if len(p.peFrames)==0||p.localDelimiter("?>"){p.pi();return}
 if p.err!=nil{return}
 id:=p.inputID;p.advance(2);target:=p.name()
 if target==""{p.event("XML_ERR_PI_NOT_STARTED","xmlParsePI : no target name");return};p.checkPITarget(target)
 separated:=p.spaces();if p.popDTDInput(){separated=true}
 if !separated&&!p.has("?>"){p.event("XML_ERR_SPACE_REQUIRED",p.joinMessage("ParsePI: PI ",target," space expected"))}
 size:=0
 for p.err==nil {
  if p.popDTDInput(){continue}
  if p.has("?>") {
   if id!=p.inputID{p.event("XML_ERR_ENTITY_BOUNDARY","PI declaration doesn't start and stop in the same entity")}
   p.advance(2);return
  }
  r,n:=p.currentNormalizedCharacter();if n==0||!xmlChar(r){p.event("XML_ERR_PI_NOT_FINISHED",p.joinMessage("ParsePI: PI ",target," never end ..."));return}
  p.advanceCharacter(r,n);size+=n;p.currentNormalizedCharacter()
  if size>maxText{p.event("XML_ERR_PI_NOT_FINISHED",p.joinMessage("PI ",target," too big found"));return}
 }
}
func (p *parser) checkPITarget(target string) {
 if len(target)>=3&&(target[0]=='x'||target[0]=='X')&&(target[1]=='m'||target[1]=='M')&&(target[2]=='l'||target[2]=='L') {
  if len(target)==3 {
   if target=="xml"{p.event("XML_ERR_RESERVED_XML_NAME","XML declaration allowed only at the start of the document")}else{p.event("XML_ERR_RESERVED_XML_NAME","Invalid PI name")}
  }else if target!="xml-stylesheet"&&target!="xml-model"{p.eventLevel("XML_ERR_RESERVED_XML_NAME","xmlParsePITarget: invalid name prefix 'xml'",Warning)}
 }
}
