package xml

import (
 "strings"
 "unicode/utf8"
)

func (p *parser) attributeListDeclaration()string {
 p.requiredDTDspace("Space required after '<!ATTLIST'")
 element:=p.name();if element==""{p.event("XML_ERR_NAME_REQUIRED","ATTLIST: no name for Element");return ""}
 p.dtdSpaces()
 for p.byte()!='>'&&p.err==nil&&!p.stopped {
  name:=p.name();if name==""{p.event("XML_ERR_NAME_REQUIRED","ATTLIST: no name for Attribute");break}
  if !p.requiredDTDspace("Space required after the attribute name"){break}
  p.dtdState.currentMembers=nil
  typ,ok:=p.attributeType();if p.err!=nil{return element};if !ok{break}
  if !p.requiredDTDspace("Space required after the attribute type"){break}
  supplied:=true;value:="";defaultKind:=""
  switch {
  case p.has("#REQUIRED"):p.advance(9);supplied=false;defaultKind="#REQUIRED"
  case p.has("#IMPLIED"):p.advance(8);supplied=false;defaultKind="#IMPLIED"
  default:
   if p.has("#FIXED"){defaultKind="#FIXED";p.advance(6);p.requiredDTDspace("Space required after '#FIXED'")}
   facts:=namespaceValue{capture:true,start:p.i+1,end:p.i+1}
   valid:=p.attribute(&facts,false)
   if !valid {
    if p.err==nil{code:=p.lastEntityError;if code==""{code="XML_ERR_ATTRIBUTE_NOT_STARTED"};p.event(code,"Attribute default value declaration error")}
    supplied=false
   }else{
    value=p.s[facts.start:facts.end];if facts.transformed{value=facts.uri.String()}
    if typ!="CDATA"{value=p.normalizeTokens(value)}
   }
  }
  if p.err!=nil{return element}
  if p.byte()!='>'&&!p.dtdSpaces(){p.event("XML_ERR_SPACE_REQUIRED","Space required after the attribute default value");break}
  definitions:=p.dtdState.attributes[element];found:=false
  for _,d:=range definitions{p.err=p.ctx.Err();if p.err!=nil{return element};if p.equalName(d.name,name){found=true;break}}
  local:=p.dtdLocalName(name);if p.err!=nil{return element}
  definition:=attributeDefinition{name:name,local:local,typ:typ,value:value,supplied:supplied,defaultKind:defaultKind,members:p.dtdState.currentMembers}
  if !found {p.dtdState.attributes[element]=checkedAppend(p,definitions,definition)}
  if p.tree!=nil&&!found{p.dtdState.currentAttributes=checkedAppend(p,p.dtdState.currentAttributes,definition)}
  if p.byte()=='>'{break}
 }
 if p.err==nil&&p.byte()=='>'{p.declarationEnd("Attribute list",p.declarationInput)};return element
}
func (p *parser) attributeType()(string,bool) {
 // Membership scratch belongs to this one attribute type, not the declaration
 // registry or saved ordered members. Validation never allocates the index.
 if p.tree!=nil {
  p.dtdState.currentMemberHashes=nil;p.dtdState.currentMemberLinks=nil
  defer func(){p.dtdState.currentMemberHashes=nil;p.dtdState.currentMemberLinks=nil}()
 }
 // Prefix recognition deliberately has no token-boundary condition.
 for _,typ:=range []string{"CDATA","IDREFS","IDREF","ID","ENTITY","ENTITIES","NMTOKENS","NMTOKEN"} {
  if p.has(typ){p.advance(len(typ));return typ,true}
 }
 notation:=false
 if p.has("NOTATION"){notation=true;p.advance(8);if !p.requiredDTDspace("Space required after 'NOTATION'"){return "",false}}
 if p.byte()!='(' {
  if notation{p.event("XML_ERR_NOTATION_NOT_STARTED","NOTATION: Name expected here")}else{p.event("XML_ERR_ATTLIST_NOT_STARTED","'(' required to start ATTLIST enumeration")}
  return "",false
 }
 p.advance(1);p.dtdSpaces();built:=false
 for p.err==nil&&!p.stopped {
  token:="";if notation{token=p.name()}else{token,_=p.scanName(true,true,"NmToken")}
  if token=="" {
   if notation{p.event("XML_ERR_NAME_REQUIRED","Name expected in NOTATION declaration");return "",false}
   p.event("XML_ERR_NMTOKEN_REQUIRED","NmToken expected in ATTLIST enumeration")
   if built{return "ENUMERATION",true};return "",false
  }
  built=true
  if p.tree!=nil{p.collectEnumerationMember(token)}
  p.dtdSpaces();if p.byte()=='|'{p.advance(1);p.dtdSpaces();continue}
  if p.byte()==')'{p.advance(1);if notation{return "NOTATION",true};return "ENUMERATION",true}
  if notation{p.event("XML_ERR_NOTATION_NOT_FINISHED","'>' required to close NOTATION declaration");return "",false}
  p.event("XML_ERR_ATTLIST_NOT_FINISHED","')' required to finish ATTLIST enumeration");return "ENUMERATION",true
 }
 return "",false
}

func (p *parser) collectEnumerationMember(token string){
 // Match namespace interning: hash in bounded chunks, use an integer-keyed
 // index, and confirm hash collisions with cancellable exact comparisons.
 hash:=uint64(0)
 for at:=range len(token){
  if at&4095==0{p.err=p.ctx.Err();if p.err!=nil{return}}
  hash=(hash^uint64(token[at]))*1099511628211
 }
 state:=p.dtdState
 for id:=state.currentMemberHashes[hash];id!=0;id=state.currentMemberLinks[id-1]{
  p.err=p.ctx.Err();if p.err!=nil{return}
  if p.equalName(state.currentMembers[id-1],token){return}
  if p.err!=nil{return}
 }
 p.err=p.ctx.Err();if p.err!=nil{return}
 if state.currentMemberHashes==nil{state.currentMemberHashes=make(map[uint64]int)}
 state.currentMemberLinks=checkedAppend(p,state.currentMemberLinks,state.currentMemberHashes[hash]);if p.err!=nil{return}
 state.currentMembers=checkedAppend(p,state.currentMembers,token);if p.err!=nil{return}
 state.currentMemberHashes[hash]=len(state.currentMembers)
}
func (p *parser) normalizeTokens(value string)string {
 if p.err!=nil{return ""}
 first:=-1
 for at:=range len(value) {
  if at&4095==0{p.err=p.ctx.Err();if p.err!=nil{return ""}}
  if value[at]==' '&&(at==0||at+1==len(value)||value[at-1]==' '){first=at;if at>0&&value[at-1]==' '{first--};break}
 }
 if first<0{p.err=p.ctx.Err();return value}
 var b strings.Builder;p.namespaceCopy(&b,value[:first]);pending:=false
 for at:=first;at<len(value)&&p.err==nil;at++ {
  if at&4095==0{p.err=p.ctx.Err();if p.err!=nil{return ""}}
  if value[at]==' '{pending=b.Len()!=0;continue}
  if pending{if !p.builderByte(&b,' '){return ""};pending=false};if !p.builderByte(&b,value[at]){return ""}
 }
 if p.err==nil{p.err=p.ctx.Err()}
 return b.String()
}
type attributeNormalization struct {
 facts *namespaceValue
 size,work,spaceStart,spaceEnd int
 pending bool
}
func (p *parser) normalizeAttributeScalar(v *attributeNormalization,r rune,start,end int) {
 width:=utf8.RuneLen(r);if r=='&'&&!p.replaceEntities{width=5}
 v.work+=width
 if v.work>=4096{p.err=p.ctx.Err();v.work=0;if p.err!=nil{return}}
 if r==' ' {
  if v.size!=0&&!v.pending{v.pending=true;v.spaceStart=start;v.spaceEnd=end}
  return
 }
 extra:=width;if v.pending{extra++}
 if !appendSize(v.size,extra){p.err=appendSizeError(v.size,extra);return}
 if v.pending{if v.facts!=nil{p.namespaceAppend(v.facts,' ',v.spaceStart,v.spaceEnd)};v.pending=false}
 v.size+=extra
 if v.facts!=nil{p.namespaceAppend(v.facts,r,start,end)}
}
func (p *parser) normalizeAttributeString(v *attributeNormalization,s string) {
 for _,r:=range s {if r=='\r'||r=='\n'||r=='\t'{r=' '};p.normalizeAttributeScalar(v,r,p.i,p.i);if p.err!=nil{return}}
}
func (p *parser) attributeDefinition(element,name string)*attributeDefinition {
 if p.dtdState==nil{return nil}
 for i:=range p.dtdState.attributes[element]{p.err=p.ctx.Err();if p.err!=nil{return nil};d:=&p.dtdState.attributes[element][i];if p.equalName(d.name,name){return d}}
 return nil
}
func (p *parser) namespaceValueSet(v *namespaceValue,value string) {
 if p.err!=nil{return}
 v.matches=3;v.length=0;v.hash=0;v.owned=true;v.value=value
 for at:=range len(value){if at&4095==0{p.err=p.ctx.Err();if p.err!=nil{return}};v.append(rune(value[at]));v.hash=(v.hash^uint64(value[at]))*1099511628211}
 if p.err==nil{p.err=p.ctx.Err()}
}
func (p *parser) applyDefaults(element,elementLocal string,attributes []ordinaryAttribute,namespaces *attributeNames,undo *[]namespaceUndo,ns *[]nodeAttribute)[]ordinaryAttribute {
 if p.dtdState==nil{return attributes}
 // Defaults use split DTD QName keys; special attribute types retain their
 // full written-name keys. QName recovery must not conflate those identities.
 declaredLocal:=p.dtdLocalName(element)
 if p.err!=nil||declaredLocal!=elementLocal{return attributes}
 for _,d:=range p.dtdState.attributes[element] {
  p.err=p.ctx.Err();if p.err!=nil{return attributes};if !d.supplied{continue}
  present:=false
  if d.name=="xmlns"||attributePrefix(d.name,d.local)=="xmlns"{present=namespaces.first==d.name;if !present&&namespaces.rest!=nil{_,present=namespaces.rest[d.name]}}else{for _,a:=range attributes{p.err=p.ctx.Err();if p.err!=nil{return attributes};if p.equalName(a.local,d.local)&&p.equalName(attributePrefix(a.name,a.local),attributePrefix(d.name,d.local)){present=true;break}}}
  if present{continue}
  if d.name=="xmlns"||attributePrefix(d.name,d.local)=="xmlns" {
   v:=namespaceValue{};p.namespaceValueSet(&v,d.value)
   if v.admitted(d.name,d.local){namespaces.add(d.name);id:=p.namespaceIntern(&v);if p.err!=nil{return attributes};if p.namespaceBindings==nil{p.namespaceBindings=make(map[string]int)};prefix:=d.local;if d.name=="xmlns"{prefix=""};*undo=checkedAppend(p,*undo,namespaceUndo{prefix,p.namespaceBindings[prefix]});if p.err!=nil{return attributes};p.namespaceBindings[prefix]=id;if p.tree!=nil{*ns=checkedAppend(p,*ns,nodeAttribute{name:d.name,local:d.local,prefix:attributePrefix(d.name,d.local),value:d.value,defaulted:true})}}
  }else{value:="";if p.tree!=nil{value=d.value};attributes=checkedAppend(p,attributes,ordinaryAttribute{name:d.name,local:d.local,value:value,defaulted:true})}
  if p.err!=nil{return attributes}
 }
 return attributes
}
func (p *parser) dtdLocalName(name string)string {
 if p.err!=nil{return ""}
 if name==""||name[0]==':'{return name}
 for at:=range len(name){if at&4095==0{p.err=p.ctx.Err();if p.err!=nil{return ""}};if name[at]==':'{return name[at+1:]}}
 if p.err==nil{p.err=p.ctx.Err()};return name
}
