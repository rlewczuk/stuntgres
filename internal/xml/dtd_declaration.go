package xml

import (
 "fmt"
)

func (p *parser) elementDeclaration()string {
 id:=p.declarationInput
 if !p.requiredDTDspace("Space required after 'ELEMENT'"){return ""}
 name:=p.name();if name==""{p.event("XML_ERR_NAME_REQUIRED","xmlParseElementDecl: no name for Element");return ""}
 p.requiredDTDspace("Space required after the element name")
 switch {
 case p.has("EMPTY"):p.advance(5)
 case p.has("ANY"):p.advance(3)
 case p.byte()=='(':
  groupID:=p.inputID;p.advance(1);p.dtdSpaces()
  if p.has("#PCDATA") {p.mixedModel(groupID)}else{p.childrenModel(1,groupID)}
 default:
  if p.byte()=='%'&&p.inputID==0 {
   // Placement errors do not consume the reference: the subset production
   // subsequently pushes it as a genuine input during recovery.
   p.event("XML_ERR_PEREF_IN_INT_SUBSET","PEReference: forbidden within markup decl in internal subset")
   return name
  }
  p.event("XML_ERR_ELEMCONTENT_NOT_STARTED","xmlParseElementDecl: 'EMPTY', 'ANY' or '(' expected")
 }
 p.declarationEnd("Element",id)
 return name
}
func (p *parser) mixedModel(id int) {
 p.advance(7);p.dtdSpaces();names:=false
 for p.byte()=='|'&&p.err==nil {names=true;p.advance(1);p.dtdSpaces();if p.name()==""{p.event("XML_ERR_NAME_REQUIRED","xmlParseElementMixedContentDecl : Name expected");return};p.dtdSpaces()}
 if p.byte()==')'&&(!names||p.has(")*")) {
  if id!=p.inputID{p.event("XML_ERR_ENTITY_BOUNDARY","Element content declaration doesn't start and stop in the same entity")}
  p.advance(1);if p.byte()=='*'{p.advance(1)};return
 }
 p.event("XML_ERR_MIXED_NOT_STARTED","MixedContentDecl : '|' or ')*' expected")
}
// Called after the opening parenthesis; each group owns its input identity.
func (p *parser) childrenModel(depth,id int)bool {
 if depth>128{p.event("XML_ERR_ELEMCONTENT_NOT_FINISHED",fmt.Sprintf("xmlParseElementChildrenContentDecl : depth %d too deep, use XML_PARSE_HUGE",depth));return false}
 if !p.contentParticle(depth){return false}
 p.dtdSpaces();separator:=p.byte()
 if separator!=','&&separator!='|'&&separator!=')'{p.event("XML_ERR_ELEMCONTENT_NOT_FINISHED","ContentDecl : ',' '|' or ')' expected");return false}
 for p.byte()==separator&&separator!=')'&&p.err==nil {p.advance(1);p.dtdSpaces();if !p.contentParticle(depth){return false};p.dtdSpaces()}
 if p.byte()!=')'{p.event("XML_ERR_SEPARATOR_REQUIRED",fmt.Sprintf("xmlParseElementChildrenContentDecl : '%c' expected",separator));return false}
 if id!=p.inputID{p.event("XML_ERR_ENTITY_BOUNDARY","Element content declaration doesn't start and stop in the same entity")}
 p.advance(1);p.occurrence();return true
}
func (p *parser) contentParticle(depth int)bool {
 p.dtdSpaces()
 if p.byte()=='(' {id:=p.inputID;p.advance(1);p.dtdSpaces();return p.childrenModel(depth+1,id)}
 if p.name()==""{p.event("XML_ERR_ELEMCONTENT_NOT_STARTED","ContentDecl : Name or '(' expected");return false}
 p.occurrence();return true
}
func (p *parser) occurrence(){if p.byte()=='?'||p.byte()=='*'||p.byte()=='+'{p.advance(1)}}
func (p *parser) notationDeclaration()(name,publicID,systemID string) {
 id:=p.declarationInput
 if !p.requiredDTDspace("Space required after '<!NOTATION'"){return}
 name=p.name();if name==""{p.event("XML_ERR_NOTATION_NOT_STARTED","NOTATION: Name expected here");return}
 if !p.requiredDTDspace("Space required after the NOTATION name'"){return}
 var ok bool;publicID,systemID,ok=p.externalID(true)
 p.dtdSpaces()
 if p.byte()!='>'{p.event("XML_ERR_NOTATION_NOT_FINISHED","'>' required to close NOTATION declaration");return}
 if p.declarationEnd("Notation",id)&&!ok&&!p.illFormed{p.event("XML_ERR_NOTATION_PROCESSING",p.joinMessage("SAX.xmlSAX2NotationDecl(",name,") externalID or PublicID missing"))};return
}
func (p *parser) entityDeclaration()(name,value,publicID,systemID,notation string,parameter,external,unparsed bool) {
 id:=p.declarationInput
 // ENTITY separator errors continue; ELEMENT/NOTATION have their own returns.
 p.requiredDTDspace("Space required after '<!ENTITY'")
 if p.byte()=='%' {parameter=true;p.advance(1);p.requiredDTDspace("Space required after '%%'")}
 name=p.name();if name==""{p.event("XML_ERR_NAME_REQUIRED","xmlParseEntityDecl: no name");return}
 p.requiredDTDspace("Space required after the entity name")
 valid:=true
 if p.byte()=='\''||p.byte()=='"' {value,valid=p.entityValue()}else{
  external=true;publicID,systemID,valid=p.externalID(false)
  if !valid&&publicID==""{p.event("XML_ERR_VALUE_REQUIRED","Entity value required")}
  uriValid,fragment:=p.entityURI(systemID)
  if !uriValid{p.eventLevel("XML_ERR_INVALID_URI",p.joinMessage("Invalid URI: ",systemID),Error);if parameter{valid=false}
  }else if fragment{p.event("XML_ERR_URI_FRAGMENT","Fragment not allowed");valid=false}
  // Check any non-'>' general continuation, not just a recognized NDATA.
  // A fatal separator error still permits the remaining syntax to be parsed.
  if parameter{p.dtdSpaces()}else if p.byte()!='>'&&!p.dtdSpaces(){p.event("XML_ERR_SPACE_REQUIRED","Space required before 'NDATA'")}
  if !parameter&&p.has("NDATA") {unparsed=true;p.advance(5);p.requiredDTDspace("Space required after 'NDATA'");notation=p.name()}
 }
 p.dtdSpaces()
 if p.byte()!='>' {p.event("XML_ERR_ENTITY_NOT_FINISHED",p.joinMessage("xmlParseEntityDecl: entity ",name," not terminated"));p.stopped=true;return}
 if p.inputID!=id{p.event("XML_ERR_ENTITY_BOUNDARY","Entity declaration doesn't start and stop in the same entity")}
 p.advance(1)
 if valid&&p.err==nil&&!p.saxDisabled {
  table:=p.dtdState.general;if parameter{table=p.dtdState.parameter}
  if table[name]==nil {table[name]=&entity{name:name,value:value,publicID:publicID,systemID:systemID,notation:notation,external:external,parameter:parameter,unparsed:unparsed}}
 }
 return
}
// Scan the quote in its owning input first, then decode numeric/PE references.
// General references are retained, including forward and unresolved references.
func (p *parser) entityValue()(string,bool) {
 quote:=p.byte();p.advance(1);start:=p.i;size:=0
 references,decode,normalize:=false,false,false
 for p.i<len(p.s)&&p.byte()!=quote&&p.poll(){
  if p.byte()=='&'||p.byte()=='%'{references=true}
  if p.has("&#")||p.byte()=='%'{decode=true}
  if p.byte()=='\r'{normalize=true}
  r,n:=p.currentNormalizedCharacter();if n==0||!xmlChar(r){break}
  size+=n;p.advanceCharacter(r,n)
  if p.byte()=='\r'{normalize=true}
  p.currentNormalizedCharacter()
  if size>maxText {p.event("XML_ERR_ENTITY_NOT_FINISHED","entity value too long");p.stopped=true;return "",false}
 }
 if p.byte()!=quote{p.event("XML_ERR_ENTITY_NOT_FINISHED","EntityValue: \" or ' expected");return "",false}
 raw:=p.s[start:p.i];if normalize{raw=p.normalizeLines(raw)};p.advance(1)
 if references&&!p.validateEntityValueReferences(raw){return "",false}
 if !decode{return raw,p.err==nil}
 value,_,_,ok:=p.decodeEntityString(raw,substituteParameter,1,true,nil)
 return value,ok
}
func (p *parser) stringCharRef(s string,at int)(rune,int,bool) {
 base:=uint32(10);if at<len(s)&&s[at]=='x'{base=16;at++};var value uint32;valid:=true;digits:=0
 for at<len(s)&&s[at]!=';' {
  if at&4095==0{p.err=p.ctx.Err();if p.err!=nil{return 0,at,false}}
  c:=s[at];v:=uint32(99);if c>='0'&&c<='9'{v=uint32(c-'0')}else if base==16&&c>='a'&&c<='f'{v=uint32(c-'a'+10)}else if base==16&&c>='A'&&c<='F'{v=uint32(c-'A'+10)}
  if v>=base{valid=false;break};if value>(0x110000-v)/base{value=0x110000}else{value=value*base+v};at++;digits++
 }
 if at<len(s)&&s[at]==';'{at++}else{valid=false}
 if !valid||digits==0 {
  code,message:=Condition("XML_ERR_INVALID_DEC_CHARREF"),"CharRef: invalid decimal value"
  if base==16{code="XML_ERR_INVALID_HEX_CHARREF";message="CharRef: invalid hexadecimal value"}
  p.event(code,message);value=0
 }
 if value>=0x110000{p.event("XML_ERR_INVALID_CHAR","xmlParseStringCharRef: character reference out of bounds");return 0,at,false}
 if !xmlChar(rune(value)){p.event("XML_ERR_INVALID_CHAR",fmt.Sprintf("xmlParseStringCharRef: invalid xmlChar value %d",value));return 0,at,false}
 return rune(value),at,true
}
func (p *parser) decodeParameterValue(e *entity,depth int)(string,bool) {
 p.err=p.ctx.Err();if p.err!=nil{return "",false}
 value,_,_,ok:=p.decodeEntityString(e.value,substituteParameter,depth,true,nil)
 return value,ok
}
