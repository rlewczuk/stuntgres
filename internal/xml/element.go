package xml

import (
 "fmt"
)

func (p *parser) element() {
 limit:=256
 if p.effective==Document {limit++}
 // xmlParseElementStart checks the existing open-name depth before '<'.
 if p.depth>=limit {p.event("XML_ERR_INTERNAL_ERROR","Excessive depth in document: 256 use XML_PARSE_HUGE option");p.stopped=true;return}
 p.advance(1)
 line:=p.line
 name,local:=p.qname()
 if p.err!=nil{return}
 if name=="" {
  p.event("XML_ERR_NAME_REQUIRED","StartTag: invalid element name")
  if p.effective==Content||p.depth>0 {p.stopped=true};return
 }
 p.depth++
 defer func(){p.depth--}()
 var namespaces attributeNames
 var firstAttribute [1]ordinaryAttribute
 attributes:=firstAttribute[:0]
 var firstUndo [1]namespaceUndo
 undo:=firstUndo[:0]
 defer func(){p.namespaceRestore(undo)}()
 p.spaces()
 if !p.fragmentGrow(){return}
 for xmlChar(rune(p.byte()))&&p.poll()&&!p.stopped {
  if p.has("/>")||p.byte()=='>' {break}
  before:=p.i
  attr,attrLocal:=p.qname()
  if p.err!=nil||p.stopped{return}
  namespace:=attr=="xmlns"||len(attr)-len(attrLocal)==6&&attr[:6]=="xmlns:"
  xmlSpace:=attrLocal=="space"&&attributePrefix(attr,attrLocal)=="xml"
  var facts *namespaceValue
  var normalized namespaceValue
  if namespace {normalized.matches=3;normalized.capture=attr!="xmlns"&&attrLocal!="xml"&&attrLocal!="xmlns";facts=&normalized}
  if xmlSpace {normalized.capture=true;normalized.construction=true;normalized.deferAmp=p.effective==Content;facts=&normalized}
  value:=false
  if attr=="" {
   p.event("XML_ERR_NAME_REQUIRED","error parsing attribute name")
  }else{
   p.spaces()
   if p.byte()!='=' {
    p.event("XML_ERR_ATTRIBUTE_WITHOUT_VALUE",p.joinMessage("Specification mandates value for attribute ",attrLocal))
   }else{
    p.advance(1);p.spaces();value=p.attribute(facts)
    if p.stopped&&p.err==nil {
     // Attribute2 restores CONTENT after the value's GROW halted input.
     // Keep the immutable-source offset while exposing the halted empty input.
     p.haltedInput=true;p.stopped=false
    }
    if xmlSpace&&p.err==nil {
     constructed:="(null)"
     if value {
      constructed=p.s[normalized.start:normalized.end]
      if normalized.transformed {constructed=normalized.uri.String()}
     }
     if constructed!="default"&&constructed!="preserve" {
      p.eventLevel("XML_WAR_SPACE_VALUE",p.joinMessage("Invalid value \"",constructed,"\" for xml:space : \"default\" or \"preserve\" expected"),Warning)
     }
    }
   }
  }
  if p.err!=nil||p.stopped{return}
  if value {
   // Admission and inline duplicate lookup precede binding insertion. A
   // duplicate declaration leaves the first admitted binding unchanged.
   if namespace {
    if normalized.admitted(attr,attrLocal) {
     if namespaces.add(attr) {
      p.event("XML_ERR_ATTRIBUTE_REDEFINED",p.joinMessage("Attribute ",attr," redefined"))
     }else if attr!="xmlns" {
      uri:=p.namespaceIntern(&normalized)
      if p.err!=nil{return}
      if p.namespaceBindings==nil {p.namespaceBindings=make(map[string]int)}
      undo=append(undo,namespaceUndo{attrLocal,p.namespaceBindings[attrLocal]})
      p.namespaceBindings[attrLocal]=uri
     }
    }
   }else{attributes=append(attributes,ordinaryAttribute{attr,attrLocal})}
  }
  // All attribute outcomes share next_attr. A supplied tag delimiter wins
  // over separator recovery, even when the preceding attribute failed.
  if !p.fragmentGrow(){return}
  if p.byte()=='>'||p.has("/>"){continue}
  if !p.spaces(){p.event("XML_ERR_SPACE_REQUIRED","attributes construct error");break}
  if p.i==before {
   p.event("XML_ERR_INTERNAL_ERROR","internal error: xmlParseStartTag: problem parsing attributes")
   break
  }
  if !p.fragmentGrow(){return}
 }
 if p.err!=nil||p.stopped{return}
 // Resolve after every declaration, including ones following attributes.
 // First-prior namespace collisions terminate the ordinary lexical search.
 p.attributeDuplicates(attributes)
 if p.err!=nil||p.stopped{return}
 if p.has("/>") {p.advance(2);p.cdataStale=false;return}
 // A QName can recover at physical input end without entering parser EOF.
 // Its incomplete opening tag still needs the start-tag, not content, error.
 if p.byte()!='>' {p.startUnfinished(local,line);return}
 p.advance(1);p.cdataStale=false
 if !p.fragmentGrow(){return}
 for p.i<len(p.s)&&p.poll()&&!p.stopped {
  if p.has("</") {
   if !p.fragmentGrow(){return}
   p.advance(2);end,matched:=p.endName(name,local)
   if p.err!=nil||p.stopped{return}
   if !p.fragmentGrow(){return}
   p.spaces()
   if p.byte()!='>' {p.event("XML_ERR_GT_REQUIRED","expected '>'")}else{p.advance(1)}
   if !matched {
    if end=="" {end="unparsable"}
    p.event("XML_ERR_TAG_NAME_MISMATCH",p.joinMessage("Opening and ending tag mismatch: ",local,fmt.Sprintf(" line %d and ",line),end))
   }
   return
  }
  before:=p.i
  switch {
  case p.has("<!--"):p.comment()
  case p.has("<?"):p.pi()
  case p.has("<![CDATA["):p.cdata()
  case p.has("<!DOCTYPE"):p.dtd();return
  case p.has("<"):p.element()
  default:p.text()
  }
  if !p.contentBoundary(){return}
  if p.i==before {break}
 }
 if p.err==nil&&!p.stopped {p.event("XML_ERR_TAG_NOT_FINISHED",p.joinMessage("Premature end of data in tag ",local,fmt.Sprintf(" line %d",line)));p.stopped=true}
}
func (p *parser) startUnfinished(name string,line int) {
 p.event("XML_ERR_GT_REQUIRED",p.joinMessage("Couldn't find end of Start Tag ",name,fmt.Sprintf(" line %d",line)))
 // A failed start tag returns to the fragment production at the same cursor.
 // DOCUMENT instead enters its epilog and reports extra content there.
 if p.byte()==0 {p.stopped=true;return}
 if p.effective==Content && p.byte()!='<' {
  r,n:=decodeUTF8(p.s[p.i:])
  if p.failedSingleByte {r=rune(p.byte());n=1}
  if n!=0&&(r==0||xmlChar(r)) {p.stopped=true}
 }
}
// The return value models a non-NULL parsed value, not error-free validation.
// Complex scalar/termination errors still return their constructed value to the
// lexical duplicate collector; missing quotes and length failures do not.
func (p *parser) attribute(facts *namespaceValue) bool {
 quote:=p.byte()
 if quote!='\''&&quote!='"' {p.event("XML_ERR_ATTRIBUTE_NOT_STARTED","AttValue: \" or ' expected");return false}
 // The fast ASCII path reports overlength at its opening quote, just as the
 // release scanner does, while the slow path counts normalized construction.
 quoteAt,quoteLine,quoteCol:=p.i,p.line,p.column
 p.advance(1)
 if facts!=nil {facts.start=p.i;facts.end=p.i}
 size:=0;fast:=true
 var current rune
 var width int
 cached:=false
 readStart:=p.i
 for p.i<len(p.s)&&p.byte()!=quote&&p.poll() {
  if p.byte()=='<' {p.event("XML_ERR_LT_IN_ATTRIBUTE","Unescaped '<' not allowed in attributes values");return true}
  if p.byte()=='&'||p.byte()<0x20||p.byte()>=128 {fast=false}
  n:=0
  contributionStart:=p.i
  var scalar rune
  if p.byte()=='&' {n,scalar=p.reference()}else{
   if cached {contributionStart=readStart}else{current,width=p.currentNormalizedCharacter()}
   if width==0 {if p.err==nil {p.event("XML_ERR_ATTRIBUTE_NOT_FINISHED","AttValue: ' expected")};return p.err==nil}
   r:=current
   if !xmlChar(r) {
    if r==0 {p.event("XML_ERR_ATTRIBUTE_NOT_FINISHED","AttValue: ' expected")}else{p.event("XML_ERR_INVALID_CHAR","invalid character in attribute value")}
    return true
   }
   n=p.advanceCharacter(r,width)
   scalar=r;if scalar=='\t'||scalar=='\n' {scalar=' '}
  }
  if p.err!=nil||p.stopped{return false}
  // Namespace comparison keeps canonical scalar bytes. CONTENT's value
  // construction instead defers both ampersand reference forms as "&#38;".
  constructed:=n
  if scalar=='&'&&p.effective==Content {constructed=5}
  size+=constructed
  contributionEnd:=p.i
  if !fast {
   // Complex AttValue commits its contribution, grows, then decodes the
   // next character before checking length. Retain that read for the next
   // iteration, including its decoder diagnostics and failure-only charset.
   if !p.fragmentGrow(){return false}
   readStart=p.i;current,width=p.currentNormalizedCharacter();cached=true
   if p.err!=nil{return false}
  }
  if size>maxText {
   if fast {p.i,p.line,p.column=quoteAt,quoteLine,quoteCol}
   p.event("XML_ERR_ATTRIBUTE_NOT_FINISHED","AttValue length too long")
   if !fast {p.event("XML_ERR_NO_MEMORY","Memory allocation failed")}
   return false
  }
  if facts!=nil&&n!=0 {p.namespaceAppend(facts,scalar,contributionStart,contributionEnd)}
 }
 if p.err!=nil{return false}
 if p.i==len(p.s) {p.event("XML_ERR_ATTRIBUTE_NOT_FINISHED","AttValue: ' expected");return true}
 p.advance(1)
 // The common next_attr boundary owns the post-value GROW. The value
 // production commits its closing quote and returns without another read.
 return true
}


// The fragment callback changes from Nop to EOF on the first near-end GROW.
// Subsequent GROW checks curBase/curEnd; document string input has no callback.
func (p *parser) fragmentGrow() bool {
 if p.haltedInput||!p.fragmentInput||p.effective!=Content||len(p.s)-p.i>=250 {return true}
 if !p.inputEOF {p.inputEOF=true;return true}
 if p.i-p.windowBase>maxText||len(p.s)-p.i>maxText {
  p.event("XML_ERR_INTERNAL_ERROR","internal error: Huge input lookup");p.stopped=true;return false
 }
 return true
}

// SHRINK changes only the simulated input window. Original and diagnostic
// offsets stay in the immutable character spelling, never a textual wrapper.
func (p *parser) contentBoundary() bool {
 if p.err!=nil||p.stopped{return false}
 if !p.fragmentGrow(){return false}
 if p.fragmentInput&&p.effective==Content&&p.i-p.windowBase>500&&len(p.s)-p.i<500 {
  p.windowBase=p.i-80
 }
 return true
}
