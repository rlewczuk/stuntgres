package xml

import (
 "math"
 "strings"
 "unicode/utf8"
)

type substitution uint8
const (substituteGeneral substitution=1<<iota;substituteParameter)

// stringName shares the release's local-string Name production among reference
// decoding and EntityValue prechecking. NCName is its length-error label, not a
// different grammar. Failure leaves the local pointer at Name begin; it never
// advances the active parser input or copies the immutable successful spelling.
func (p *parser) stringName(s string,start int)(string,int) {
 end:=start;nextPoll:=start
 for end<len(s){
  if end>=nextPoll{p.err=p.ctx.Err();if p.err!=nil{return "",start};nextPoll=end+min(4096,len(s)-end)}
  r,n:=utf8.DecodeRuneInString(s[end:])
  if end==start&&!nameStart(r)||!nameChar(r){break}
  end+=n
  if end-start>maxName{p.event("XML_ERR_NAME_TOO_LONG","Name too long: NCName");return "",start}
 }
 p.err=p.ctx.Err();if p.err!=nil{return "",start}
 return s[start:end],end
}

// growEntityString tracks libxml's logical buffer, not Builder.Cap. All output
// contributes to its per-invocation capacity; only recursive replacement copies
// invoke size accounting, at crossed growth points rather than their final size.
// Bulk ordinary runs have the same capacity history without disposable buffers.
func (p *parser) growEntityString(capacity,start,size uint64,recursive bool)(uint64,bool) {
 if p.err!=nil{return capacity,false}
 p.err=p.ctx.Err();if p.err!=nil{return capacity,false}
 for size>start&&size>capacity-100{
  // A NULL-content reserve can leave its trailing semicolon one byte beyond
  // the usual threshold. Check the next actual byte, never a stale position.
  checkpoint:=max(start+1,capacity-99)
  if recursive&&!p.entityCheck(checkpoint,nil,0){return capacity,false}
  if capacity>(math.MaxUint64-100)/2{p.err=appendSizeError(math.MaxInt,1);return capacity,false}
  capacity=capacity*2+100
  p.err=p.ctx.Err();if p.err!=nil{return capacity,false}
 }
 return capacity,true
}

// NULL content retains its local reference spelling, with a variable reserve.
// Ampersand comes first; Name and semicolon have no ordinary post-growth step.
func (p *parser) reserveEntityString(capacity,size,nameBytes uint64)(uint64,uint64,bool) {
 if p.err!=nil{return capacity,size,false}
 p.err=p.ctx.Err();if p.err!=nil{return capacity,size,false}
 if nameBytes>math.MaxUint64-2||math.MaxUint64-size<nameBytes+2{p.entityLoop();return capacity,size,false}
 next:=size+nameBytes+2
 if next>math.MaxUint64-99{p.err=appendSizeError(math.MaxInt,1);return capacity,size,false}
 grown:=capacity
 if next+99>capacity{
  increment:=nameBytes+100
  if capacity>(math.MaxUint64-increment)/2{p.err=appendSizeError(math.MaxInt,1);return capacity,size,false}
  grown=capacity*2+increment
 }
 p.err=p.ctx.Err();if p.err!=nil{return capacity,size,false}
 return grown,next,true
}

// Entity kind and content nullability are separate in the pinned string parser.
// In particular, an unparsed entity's nonNULL content is its notation name.
// This derived view leaves declaration/value identity and external loading alone.
func (e *entity) stringContent()(string,bool) {
 if !e.external{return e.value,true}
 if e.unparsed&&e.notation!=""{return e.notation,true}
 return "",false
}

// decodeEntityString is the DTD/reference string production, not an XML parser.
// Inspection counts the same recursive references without disposable expansion
// buffers. It shares depth, density and cancellation accounting with emission.
func (p *parser) decodeEntityString(s string,what substitution,depth int,emit bool,normal *attributeNormalization)(string,uint64,bool,bool) {
 if p.err!=nil{return "",0,false,false}
 p.err=p.ctx.Err();if p.err!=nil{return "",0,false,false}
 if depth>40{p.entityLoop();return "",0,false,false}
 var b strings.Builder;var size uint64;capacity:=uint64(300);hasLT:=false
 add:=func(value string,n uint64,check,contribute bool)bool {
  if math.MaxUint64-size<n{p.entityLoop();return false}
  next:=size+n
  grown,ok:=p.growEntityString(capacity,size,next,check);if !ok{return false};capacity=grown
  if normal!=nil&&contribute{p.normalizeAttributeString(normal,value);if p.err!=nil{return false}}
  if emit&&!p.appendValue(&b,value){return false};size=next;return true
 }
 for at:=0;at<len(s)&&p.err==nil; {
  p.err=p.ctx.Err();if p.err!=nil{return "",0,false,false}
  c:=s[at]
  if c=='&'&&at+1<len(s)&&s[at+1]=='#' {
   r,end,ok:=p.stringCharRef(s,at+2);if !ok{return "",0,false,false};at=end
   var data [4]byte;n:=utf8.EncodeRune(data[:],r);if r=='<'{hasLT=true};if !add(string(data[:n]),uint64(n),false,true){return "",0,false,false};continue
  }
  if c=='&'&&what&substituteGeneral!=0||c=='%'&&what&substituteParameter!=0 {
   name,end:=p.stringName(s,at+1);if p.err!=nil{return "",0,false,false}
   if name=="" {
    message:="xmlParseStringEntityRef: no name";if c=='%'{message="xmlParseStringPEReference: no name"}
    p.event("XML_ERR_NAME_REQUIRED",message)
    at=end
    if !p.entityCheck(0,nil,0){return "",0,false,false}
    continue
   }
   if end==len(s)||s[end]!=';'{
    p.event("XML_ERR_ENTITYREF_SEMICOL_MISSING","EntityRef: expecting ';'")
    // Both string-reference productions leave the failed delimiter unconsumed.
    // Their caller checks nil, then resumes without lookup/count/PE-seen work.
    at=end
    if !p.entityCheck(0,nil,0){return "",0,false,false}
    continue
   }
   at=end+1
   if c=='&'{if scalar,ok:=predefined(name);ok{if scalar=='<'{hasLT=true};if !add(string(scalar),1,false,true){return "",0,false,false};continue}}
   var e *entity
   if c=='&'{e=p.lookupEntity(name)}else{
    if !p.addEntityCount(1){return "",0,false,false}
    if p.dtdState!=nil{e=p.dtdState.parameter[name]}
    if e==nil{
     if p.declaration.Standalone==Yes||p.dtdState==nil||!p.dtdState.peReferences&&!p.dtdState.external{p.event("XML_ERR_UNDECLARED_ENTITY",p.joinMessage("PEReference: %",name,"; not found"))}else{p.eventLevel("XML_WAR_UNDECLARED_ENTITY",p.joinMessage("PEReference: %",name,"; not found"),Warning)}
     p.entityCheck(0,nil,0)
    }
    if p.dtdState!=nil{p.dtdState.peReferences=true}
   }
   // Both lookup productions and this caller check nil. A fatal missing
   // lookup alone is not a halt: keep its progressed pointer and residual.
   if e==nil{if !p.entityCheck(0,nil,0){return "",0,false,false};continue}
   if !p.inspectEntity(e,depth)||!p.entityCheck(0,e,0)||!p.addEntityCount(e.checked/2){return "",0,false,false}
   content,present:=e.stringContent()
   if !present{
    if c=='%'{continue}
    grown,next,ok:=p.reserveEntityString(capacity,size,uint64(len(name)));if !ok{return "",0,false,false}
    capacity=grown
    if normal!=nil{
     p.normalizeAttributeString(normal,"&");p.normalizeAttributeString(normal,name);p.normalizeAttributeString(normal,";")
     if p.err!=nil{return "",0,false,false}
    }
    if emit&&(!p.appendValue(&b,"&")||!p.appendValue(&b,name)||!p.appendValue(&b,";")){return "",0,false,false}
    size=next;continue
   }
   var value string;var n uint64;var lt,ok bool
   if c=='%'&&emit {
    value,ok=p.decodeParameterValue(e,depth+1);n=uint64(len(value));lt=p.containsByte(value,'<')
   }else{value,n,lt,ok=p.decodeEntityString(content,what,depth+1,emit,normal)}
   if !ok{return "",0,false,false};hasLT=hasLT||lt
   if !add(value,n,true,c=='%'){return "",0,false,false};continue
  }
  // Ordinary runs, including bypassed GE spelling, share immutable input.
  // At most one polling chunk is inspected/copied at a time.
  start:=at;end:=at+min(4096,len(s)-at)
  // Valid UTF8 needs at most three bytes of boundary adjustment. Failed
  // single-byte input needs none; never backtrack a whole continuation run
  // to the starting cursor and leave the ordinary-run loop without progress.
  if !p.failedSingleByte&&end<len(s){for back:=0;back<utf8.UTFMax-1&&!utf8.RuneStart(s[end]);back++{end--}}
  for at<end {
   if s[at]=='&'&&(what&substituteGeneral!=0||at+1<len(s)&&s[at+1]=='#')||s[at]=='%'&&what&substituteParameter!=0{break}
   if s[at]=='<'{hasLT=true};at++
  }
  if !add(s[start:at],uint64(at-start),false,true){return "",0,false,false}
 }
 if p.err==nil{p.err=p.ctx.Err()}
 return b.String(),size,hasLT,p.err==nil
}
func (p *parser) inspectEntity(e *entity,depth int)bool {
 content,present:=e.stringContent()
 if e.checked!=0||!present{return true}
 e.checked=1;old:=p.entityCount
 _,_,lt,ok:=p.decodeEntityString(content,substituteGeneral,depth+1,false,nil)
 diff:=p.entityCount-old
 if diff<uint64(math.MaxInt32/2){diff++}else{diff=uint64(math.MaxInt32/2)}
 e.checked=diff*2;if lt{e.checked|=1}
 if !ok{e.value=""}
 return ok
}
func (p *parser) containsByte(s string,c byte)bool {
 if p.err!=nil{return false}
 for at:=0;at<len(s); {
  p.err=p.ctx.Err();if p.err!=nil{return false};end:=at+min(4096,len(s)-at)
  if strings.IndexByte(s[at:end],c)>=0{return true};at=end
 }
 p.err=p.ctx.Err()
 return false
}
func (p *parser) validateEntityValueReferences(s string)bool {
 nextPoll:=0
 for at:=0;at<len(s); {
  if at>=nextPoll{p.err=p.ctx.Err();if p.err!=nil{return false};nextPoll=at+min(4096,len(s)-at)}
  c:=s[at];if c!='%'&&(c!='&'||at+1<len(s)&&s[at+1]=='#'){at++;continue}
  at++;name,end:=p.stringName(s,at);if p.err!=nil{return false};at=end
  if name==""||at==len(s)||s[at]!=';'{p.event("XML_ERR_ENTITY_CHAR_ERROR",p.joinMessage("EntityValue: '",string(c),"' forbidden except for entities references"));return false}
  if c=='%'&&p.inputID==0{p.event("XML_ERR_ENTITY_PE_INTERNAL","PEReferences forbidden in internal subset");return false};at++
 }
 if p.err==nil{p.err=p.ctx.Err()}
 return p.err==nil
}
