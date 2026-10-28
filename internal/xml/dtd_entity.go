package xml

import (
 "math"
 "strings"
)

func (p *parser) entityLoop(){p.event("XML_ERR_ENTITY_LOOP","Detected an entity reference loop")}
func (p *parser) addEntityCount(n uint64)bool {
 if p.err!=nil{return false}
 p.err=p.ctx.Err();if p.err!=nil{return false}
 if math.MaxUint64-p.entityCount<n{p.entityLoop();return false};p.entityCount+=n;return true
}
// Compare products without overflow. The release uses strict inequalities.
func productLess(a,am,b,bm uint64)bool {
 // All multipliers are small constants; a two-word product gives the exact
 // comparison even when either product exceeds a machine word.
 ah,al:=multiplySmall(a,am);bh,bl:=multiplySmall(b,bm)
 return ah<bh||ah==bh&&al<bl
}
func multiplySmall(a,b uint64)(uint64,uint64){hi,lo:=uint64(0),uint64(0);for range b{previous:=lo;lo+=a;if lo<previous{hi++}};return hi,lo}
func (p *parser) entityCheck(size uint64,e *entity,replacement uint64)bool {
 if p.lastEntityError=="XML_ERR_ENTITY_LOOP"{return false}
 if p.err!=nil{return false}
 p.err=p.ctx.Err();if p.err!=nil{return false}
 consumed:=uint64(p.i)
 if math.MaxUint64-consumed<p.entitySize{p.entityLoop();return false};consumed+=p.entitySize
 if p.inDTD&&p.entityCount>10000&&p.entityCount%1024==0 {
  total:=uint64(p.i);for _,f:=range p.peFrames{p.err=p.ctx.Err();if p.err!=nil{return false};if math.MaxUint64-total<uint64(f.i){p.entityLoop();return false};total+=uint64(f.i)}
  if productLess(total,10,p.entityCount,1){p.entityLoop();return false}
 }
 ok:=true
 switch {
 case replacement!=0:ok=replacement<maxText||productLess(replacement,1,consumed,10)
 case size!=0:ok=size<1000||productLess(size,1,consumed,10)&&productLess(p.entityCount,3,consumed,10)
 case e!=nil:ok=productLess(e.checked/2,3,consumed,10)
 default:ok=p.entityCount<=10000||p.lastEntityError!="XML_ERR_UNDECLARED_ENTITY"&&p.lastEntityError!="XML_WAR_UNDECLARED_ENTITY"
 }
 if !ok{p.entityLoop()};return ok
}
func (p *parser) lookupEntity(name string)*entity {
 if !p.addEntityCount(1){return nil}
 var e *entity;if p.dtdState!=nil{e=p.dtdState.general[name]}
 if e==nil {
  suppressed:=p.declaration.Standalone!=Yes&&p.dtdState!=nil&&(p.dtdState.external||p.dtdState.peReferences)
  if suppressed {p.eventLevel("XML_WAR_UNDECLARED_ENTITY",p.joinMessage("Entity '",name,"' not defined"),Error);if !p.inAttribute&&!p.inDTD&&p.tree!=nil{p.appendNode(&node{kind:entityReferenceNode,name:name})}}else{p.event("XML_ERR_UNDECLARED_ENTITY",p.joinMessage("Entity '",name,"' not defined"))}
  p.entityCheck(0,nil,0);return nil
 }
 if e.unparsed{p.event("XML_ERR_UNPARSED_ENTITY",p.joinMessage("Entity reference to unparsed entity ",name));return e}
 if p.inAttribute&&e.external{p.event("XML_ERR_ENTITY_IS_EXTERNAL",p.joinMessage("Attribute references external entity '",name,"'"));return e}
 if p.inAttribute&&p.containsByte(e.value,'<'){p.event("XML_ERR_LT_IN_ATTRIBUTE",p.joinMessage("'<' in entity '",name,"' is not allowed in attributes values"));return e}
 return e
}
func (p *parser) contentReference(){n,r:=p.reference();if n>0{p.scalarText(r)}}
func (p *parser) expandGeneral(e *entity) {
 if e.external{return}
 if p.entityDepth+1>40 {p.entityLoop();p.stopped=true;return}
 wasParsed:=e.parsed
 if wasParsed {
  if e.shape==0 {
   // The release reparses childless replacements. Preserve its unsaturated
   // reference count and final density check without repeating empty work.
   if p.addEntityCount(e.childReferences){p.entityCheck(0,e,0)}
   return
  }
  if !p.addEntityCount(e.checked/2){return};p.countEntityCopy(e)
  p.noteEntity(e)
  if p.tree!=nil&&p.err==nil&&!p.failed(){for _,n:=range e.nodes{if n.kind==textNode{p.appendText(n.value)}else{copy:=p.cloneNode(n);if p.err!=nil{return};p.appendNode(copy)};if p.err!=nil{return}}}
  return
 }
 child:=parser{ctx:p.ctx,s:e.value,line:1,column:1,requested:p.requested,effective:Content,replaceEntities:p.replaceEntities,priorError:p.illFormed||p.priorError,entityDepth:p.entityDepth+2,source:EntitySource,entityName:e.name,dtdState:p.dtdState,namespaceBindings:p.namespaceBindings,namespaceHashes:p.namespaceHashes,namespaceURIs:p.namespaceURIs}
 child.ignoreBlanks=p.ignoreBlanks;child.spaceState=-1
 if p.tree!=nil{child.tree=&document{};if p.ignoreBlanks{child.parent=&node{kind:elementNode}}}
 // Child parser contexts deliberately do not inherit external/PE/standalone
 // leniency. Tables and lexical namespace scope are inherited, not flags.
 if p.dtdState!=nil {state:=*p.dtdState;state.external=false;state.peReferences=false;child.dtdState=&state}
 child.top()
 if child.parent!=nil{child.tree.nodes=child.parent.children;child.parent=nil}
 if child.err==nil&&child.stopped&&child.byte()!=0&&!child.has("</")&&child.lastEntityError!="XML_ERR_ENTITY_LOOP"{child.event("XML_ERR_EXTRA_CONTENT","extra content at the end of well balanced chunk")}
 if child.err!=nil{p.err=child.err;return}
 p.namespaceBindings=child.namespaceBindings;p.namespaceHashes=child.namespaceHashes;p.namespaceURIs=child.namespaceURIs
 for _,d:=range child.events {
  p.err=p.ctx.Err();if p.err!=nil{return}
  if !appendSize(p.diagnosticBytes,len(d.Message)+len(d.SourceContext)+len(d.Caret)+64){p.err=appendSizeError(p.diagnosticBytes,len(d.Message)+len(d.SourceContext)+len(d.Caret)+64);return}
  p.diagnosticBytes+=len(d.Message)+len(d.SourceContext)+len(d.Caret)+64;p.events=checkedAppend(p,p.events,d);if p.err!=nil{return}
 }
 if child.illFormed{p.illFormed=true}
 if child.lastEntityError!=""{p.lastEntityError=child.lastEntityError}
 count:=child.entityCount
 if !p.addEntityCount(count){return}
 diff:=min(child.entityCount,uint64(math.MaxInt32/2)-1)+1;e.checked=diff*2;if p.containsByte(e.value,'<'){e.checked|=1}
 if child.lastEntityError=="XML_ERR_ENTITY_LOOP" {p.entityLoop();p.stopped=true;return}
 if !p.entityCheck(0,e,0){return}
 if child.failed(){p.event("XML_ERR_UNDECLARED_ENTITY",p.joinMessage("Entity '",e.name,"' failed to parse"));e.value="";e.nodes=nil;p.entityCheck(0,e,0);return}
 e.parsed=true;e.childReferences=child.entityCount
 e.shape=child.entityShape;if e.shape==1{e.owner=1}
 if e.shape!=0{
  // Both document and balanced-child contexts have an input. First text
  // publication (owner1/listNULL) and nontext publication (inputNr>0) therefore
  // charge the same source-byte volume as cached copies. Childless lists
  // return before this publication path in the release.
  p.countEntityCopy(e)
  if p.err!=nil||p.failed(){return}
 }
 p.noteEntity(e)
 if p.tree!=nil{
  child.finishDocument();if child.err!=nil{p.err=child.err;return}
  e.nodes=child.tree.nodes
  for _,n:=range e.nodes{if n.kind==textNode{p.appendText(n.value)}else{p.appendNode(n)};if p.err!=nil{return}}
 }
}
func (p *parser) countEntityCopy(e *entity) {
 n:=uint64(len(e.value))+5
 if math.MaxUint64-p.entityCopies<n{p.entityLoop();return};p.entityCopies+=n;p.entityCheck(0,nil,p.entityCopies)
}
func (p *parser) decodeAttributeEntity(e *entity,depth int)(string,bool) {
 p.err=p.ctx.Err();if p.err!=nil{return "",false}
 if !p.addEntityCount(e.owner){return "",false}
 value,size,_,ok:=p.decodeEntityString(e.value,substituteGeneral,depth+1,p.attributeCapture&&p.attributeNormalization==nil,p.attributeNormalization)
 if !ok{return "",false}
 if p.attributeNormalization!=nil{return "",true}
 if size>uint64(math.MaxInt){p.err=appendSizeError(math.MaxInt,1);return "",false}
 p.attributeReplacementSize=int(size)
 if !p.attributeCapture{return "",true}
 first:=-1
 for at:=range len(value){if at&4095==0{p.err=p.ctx.Err();if p.err!=nil{return "",false}};if value[at]=='\t'||value[at]=='\n'||value[at]=='\r'{first=at;break}}
 if first<0{p.err=p.ctx.Err();return value,p.err==nil}
 var b strings.Builder;if !p.ensureBuilder(&b,len(value)){return "",false};p.namespaceCopy(&b,value[:first])
 for at:=first;at<len(value)&&p.err==nil;at++{if at&4095==0{p.err=p.ctx.Err();if p.err!=nil{return "",false}};c:=value[at];if c=='\r'||c=='\t'||c=='\n'{c=' '};b.WriteByte(c)}
 if p.err==nil{p.err=p.ctx.Err()}
 return b.String(),p.err==nil
}
func predefined(name string)(rune,bool){switch name{case "amp":return '&',true;case "lt":return '<',true;case "gt":return '>',true;case "apos":return '\'',true;case "quot":return '"',true};return 0,false}
