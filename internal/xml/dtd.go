package xml

import (
 "strings"
)

type entity struct {name,value,publicID,systemID,notation string; external,parameter,unparsed bool; checked,owner,childReferences uint64; parsed bool;nodes []*node;shape uint8}
type attributeDefinition struct {name,local,typ,value string; supplied bool; defaultKind string; members []string}
type dtdState struct {
 general,parameter map[string]*entity
 attributes map[string][]attributeDefinition
 external,peReferences bool
 doctype *doctype
 currentAttributes []attributeDefinition
 elementTypes map[string]string
 currentModel *contentModel
 currentType,originalEntityValue string
 currentMembers []string
 currentMemberHashes map[uint64]int
 currentMemberLinks []int
 currentPublic,currentSystem,currentAdmitted bool
 notations map[string]bool
}
type inputFrame struct {s string;i,base,line,column,nextPoll,inputID int; source DiagnosticSource;entityName string}
func (p *parser) frame()inputFrame{return inputFrame{p.s,p.i,p.base,p.line,p.column,p.nextPoll,p.inputID,p.source,p.entityName}}
func (p *parser) restoreFrame(f inputFrame){p.s=f.s;p.i=f.i;p.base=f.base;p.line=f.line;p.column=f.column;p.nextPoll=min(f.nextPoll,f.i);p.inputID=f.inputID;p.source=f.source;p.entityName=f.entityName}
func (p *parser) dtdInit(){if p.dtdState==nil{p.dtdState=&dtdState{general:make(map[string]*entity),parameter:make(map[string]*entity),attributes:make(map[string][]attributeDefinition),elementTypes:make(map[string]string)}}}

// Lookahead has no grammar side effects. The first double hyphen must terminate
// a comment, exactly as xml_doctype_in_content; malformed misc cannot promote.
func (p *parser) leadingDoctype(at int)bool {
 for at<len(p.s) {
  p.err=p.ctx.Err();if p.err!=nil{return false}
  if space(p.s[at]){at++;continue}
  marker:="";skip:=0
  if strings.HasPrefix(p.s[at:],"<!--"){marker="--";skip=4}else if strings.HasPrefix(p.s[at:],"<?"){marker="?>";skip=2}else{return strings.HasPrefix(p.s[at:],"<!DOCTYPE")}
  at+=skip
  for at<len(p.s)&&!strings.HasPrefix(p.s[at:],marker){if at&4095==0{p.err=p.ctx.Err();if p.err!=nil{return false}};at++}
  if at==len(p.s){return false}
  at+=len(marker)
  if marker=="--"{if at==len(p.s)||p.s[at]!='>'{return false};at++}
 }
 return false
}
func (p *parser) dtd() {
 p.dtdInit();p.inDTD=true;defer func(){p.inDTD=false}()
 rawStart:=p.i
 p.advance(9);p.spaces();name:=p.name()
 if name==""{p.event("XML_ERR_NAME_REQUIRED","xmlParseDocTypeDecl : no DOCTYPE name !")}
 d:=&doctype{name:name};p.dtdState.doctype=d
 p.spaces()
 if p.has("SYSTEM")||p.has("PUBLIC") {d.external=true;d.public=p.has("PUBLIC");d.publicID,d.systemID,_=p.externalID(false);p.dtdState.external=true;p.spaces()}
 subset:=p.byte()=='['
 d.hasSubset=subset
 if p.byte()=='[' {
  p.advance(1);start:=p.i
  p.internalSubset()
  if p.err!=nil||p.stopped{return}
  if p.inputID==0&&p.i>=start{d.subset=p.s[start:p.i]}
  if p.byte()==']'{p.advance(1);p.spaces()}
 }
 if p.byte()!='>'{p.event("XML_ERR_DOCTYPE_NOT_FINISHED","DOCTYPE improperly terminated");if !subset&&p.byte()!=0{p.advance(1)};return}
 p.advance(1)
 d.raw=p.s[rawStart:p.i]
 if p.tree!=nil{p.appendNode(&node{kind:doctypeNode,name:name,doctype:d})}
}
func (p *parser) internalSubset() {
 for p.err==nil&&!p.stopped {
  p.dtdSpaces()
  if p.err!=nil||p.stopped{return}
  if p.byte()==']'&&len(p.peFrames)==0{return}
  before,id:=p.i,p.inputID
  switch {
  case p.has("<!--"):p.dtdComment()
  case p.has("<?"):p.dtdPI()
  case p.has("<!ELEMENT"):p.dtdDeclaration("ELEMENT")
  case p.has("<!ATTLIST"):p.dtdDeclaration("ATTLIST")
  case p.has("<!ENTITY"):p.dtdDeclaration("ENTITY")
  case p.has("<!NOTATION"):p.dtdDeclaration("NOTATION")
  case p.byte()=='%':p.parameterReference()
  default:
   p.subsetError()
   if len(p.peFrames)==0{return}
   f:=p.peFrames[len(p.peFrames)-1];p.peFrames=p.peFrames[:len(p.peFrames)-1];p.restoreFrame(f);continue
  }
  if p.err!=nil||p.stopped{return}
  if p.byte()=='%' {p.parameterReference()}
  if p.i==before&&p.inputID==id {p.subsetError();return}
 }
}
func (p *parser) subsetError(){p.event("XML_ERR_INTERNAL_ERROR","internal error: xmlParseInternalSubset: error detected in Markup declaration")}
func (p *parser) dtdSpaces()bool {
 any:=false
 for p.err==nil&&!p.stopped {
  if p.spaces(){any=true}
  if p.i==len(p.s)&&len(p.peFrames)>0 {
   f:=p.peFrames[len(p.peFrames)-1];p.peFrames=p.peFrames[:len(p.peFrames)-1];p.restoreFrame(f);any=true;continue
  }
  if p.byte()=='%'&&p.inDTD&&p.i+1<len(p.s)&&!space(p.s[p.i+1]) {
   // PE recognition is restricted to token boundaries; declaration values and
   // identifiers have their own quoted grammar and do not call this routine.
   if p.inputID==0&&p.declarationInput>=0 {break}
   p.parameterReference();any=true;continue
  }
  return any
 }
 return any
}
func (p *parser) requiredDTDspace(message string)bool {if !p.dtdSpaces(){p.event("XML_ERR_SPACE_REQUIRED",message);return false};return true}
func (p *parser) parameterReference() {
 if p.err!=nil{return}
 p.err=p.ctx.Err();if p.err!=nil{return}
 p.advance(1);name:=p.name()
 if name==""{p.event("XML_ERR_PEREF_NO_NAME","PEReference: no name");return}
 if p.byte()!=';'{p.event("XML_ERR_PEREF_SEMICOL_MISSING","PEReference: expecting ';'");return};p.advance(1)
 if !p.addEntityCount(1){return}
 e:=p.dtdState.parameter[name]
 if e==nil {
  if !p.dtdState.external&&!p.dtdState.peReferences||p.declaration.Standalone==Yes {p.event("XML_ERR_UNDECLARED_ENTITY",p.joinMessage("PEReference: %",name,"; not found"))}else{
   // Suppression records the identity before accounting, without constructing
   // a message that eventLevel deliberately never publishes.
   p.eventLevel("XML_WAR_UNDECLARED_ENTITY","",Warning)
  }
  p.entityCheck(0,nil,0);p.dtdState.peReferences=true;return
 }
 if !p.inspectEntity(e,p.entityDepth)||!p.entityCheck(0,e,0){return}
 if e.external{p.dtdState.peReferences=true;return}
 if len(p.peFrames)>=40 {p.entityLoop();p.stopped=true;return}
 p.err=p.ctx.Err();if p.err!=nil{return}
 p.peFrames=append(p.peFrames,p.frame());p.nextInputID++;p.inputID=p.nextInputID
 p.s=e.value;p.i=0;p.base=0;p.line=1;p.column=1;p.nextPoll=0;p.source=EntitySource;p.entityName=name
 p.dtdState.peReferences=true
}
func (p *parser) dtdDeclaration(kind string) {
 start,id,source:=p.i,p.inputID,p.s
 old:=p.declarationInput;p.declarationInput=id;defer func(){p.declarationInput=old}()
 p.advance(len(kind)+2)
 var name,value,publicID,systemID,notation string;parameter,external,unparsed:=false,false,false
 p.dtdState.currentAttributes=nil
 p.dtdState.currentModel=nil;p.dtdState.currentType="";p.dtdState.originalEntityValue=""
 p.dtdState.currentPublic=false;p.dtdState.currentSystem=false;p.dtdState.currentAdmitted=true
 switch kind {
 case "ELEMENT":name=p.elementDeclaration()
 case "ATTLIST":name=p.attributeListDeclaration()
 case "ENTITY":name,value,publicID,systemID,notation,parameter,external,unparsed=p.entityDeclaration()
 case "NOTATION":name,publicID,systemID=p.notationDeclaration()
 }
 if p.err!=nil{return}
 if kind=="ELEMENT"&&name!="" {if _,exists:=p.dtdState.elementTypes[name];!exists{p.dtdState.elementTypes[name]=p.dtdState.currentType}else{p.dtdState.currentAdmitted=false}}
 if kind=="NOTATION"&&p.tree!=nil {if p.dtdState.notations==nil{p.dtdState.notations=make(map[string]bool)};if p.dtdState.notations[name]{p.dtdState.currentAdmitted=false};p.dtdState.notations[name]=true}
 if p.inputID==id&&p.i>start&&p.i<=len(source)&&p.tree!=nil&&p.dtdState.currentAdmitted {
  p.dtdState.doctype.declarations=checkedAppend(p,p.dtdState.doctype.declarations,dtdDeclaration{kind:kind,name:name,raw:source[start:p.i],value:value,originalValue:p.dtdState.originalEntityValue,publicID:publicID,systemID:systemID,notation:notation,parameter:parameter,external:external,unparsed:unparsed,attributes:p.dtdState.currentAttributes,model:p.dtdState.currentModel,elementType:p.dtdState.currentType,public:p.dtdState.currentPublic,systemPresent:p.dtdState.currentSystem})
 }
}
func (p *parser) declarationEnd(kind string,id int)bool {
 p.dtdSpaces()
 if p.byte()!='>' {p.event("XML_ERR_GT_REQUIRED","expected '>'");return false}
 if p.inputID!=id {p.event("XML_ERR_ENTITY_BOUNDARY",kind+" declaration doesn't start and stop in the same entity")}
 p.advance(1);return true
}
func (p *parser) externalID(notation bool)(publicID,systemID string,ok bool) {
 if p.has("SYSTEM") {
  p.dtdState.currentSystem=true
  p.advance(6);if !p.dtdSpaces(){p.event("XML_ERR_SPACE_REQUIRED","Space required after 'SYSTEM'")}
  systemID,ok=p.dtdLiteral(false)
 }else if p.has("PUBLIC") {
  p.dtdState.currentPublic=true
  p.advance(6);if !p.dtdSpaces(){p.event("XML_ERR_SPACE_REQUIRED","Space required after 'PUBLIC'")}
  publicID,ok=p.dtdLiteral(true)
  if !ok{p.event("XML_ERR_PUBID_REQUIRED","PUBLIC, the Public Identifier is missing");return}
  separated:=p.dtdSpaces()
  if notation&&p.byte()!='\''&&p.byte()!='"'{return publicID,"",true}
  if !separated{p.event("XML_ERR_SPACE_REQUIRED","Space required after the Public Identifier")}
  p.dtdState.currentSystem=true
  systemID,ok=p.dtdLiteral(false)
 }else{return "","",false}
 if !ok{p.event("XML_ERR_URI_REQUIRED","SYSTEM or PUBLIC, the URI is missing")}
 return
}
func (p *parser) dtdLiteral(public bool)(string,bool) {
 quote:=p.byte();if quote!='\''&&quote!='"'{p.event("XML_ERR_LITERAL_NOT_STARTED","SystemLiteral \" or ' expected");return "",false}
 p.advance(1);start:=p.i;size:=0
 for p.i<len(p.s)&&p.byte()!=quote&&p.poll(){
  if public {
   r:=rune(p.byte())
   if !(r==' '||r=='\r'||r=='\n'||r>='a'&&r<='z'||r>='A'&&r<='Z'||r>='0'&&r<='9'||strings.ContainsRune("-'()+,./:=?;!*#@$_%",r)){break}
   p.advance(1);size++
  }else{
   r,n:=p.currentNormalizedCharacter();if n==0||!xmlChar(r){break}
   p.advanceCharacter(r,n);size+=n;p.currentNormalizedCharacter()
  }
  if size>maxName {label:="SystemLiteral";if public{label="Public ID"};p.event("XML_ERR_NAME_TOO_LONG","Name too long: "+label);return "",false}
 }
 value:=p.s[start:p.i];if !public{value=p.normalizeLines(value)}
 if p.byte()!=quote{p.event("XML_ERR_LITERAL_NOT_FINISHED","Unfinished System or Public ID \" or ' expected");return value,true}
 p.advance(1);return value,true
}
