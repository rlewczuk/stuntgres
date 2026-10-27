package xml

import (
 "fmt"
 "strings"
 "unicode/utf8"
)

func (p *parser) comment() {
 start,id:=p.i,p.inputID
 defer func(){
  if p.err==nil&&id==p.inputID&&p.i>=start+7&&p.s[p.i-3:p.i]=="-->" {p.collectMarkup(commentNode,"",start+4,p.i-3,start)}
 }()
 defer func(){p.cdataStale=false}()
 p.advance(4);data:=p.i
 size:=0;committed:=p.i
 for p.poll() {
  // Scan the complete local ASCII segment. Line/column advance immediately,
  // but the input cursor is committed only after the segment's length check.
  for p.poll() {
   b:=p.byte()
   if b=='\n'||b=='\t'||b>=0x20&&b<=0x7f&&b!='-' {p.advance(1)}else{break}
  }
  if p.err!=nil{return}
  size+=p.i-committed
  if size>maxText {
   p.i=committed;p.nextPoll=min(p.nextPoll,p.i)
   p.event("XML_ERR_COMMENT_NOT_FINISHED","Comment too big found");return
  }
  committed=p.i
  if p.byte()=='\r'&&len(p.s)-p.i>1&&p.s[p.i+1]=='\n' {
   // The accelerator skips CR and retains LF as the next segment origin.
   p.i++;committed=p.i;p.advance(1);continue
  }
  if p.has("-->") {p.advance(3);return}
  if p.byte()=='-' {
   if p.has("--") {
    message:="Double hyphen within comment"
    // Positive committed payload means a buffer exists, even when trimming
    // its displayed newline preview leaves nothing. The opener is separate.
    if size>0 {message+=": <!--"+strings.TrimRight(p.commentPreview(data,p.i),"\n")}
    p.event("XML_ERR_HYPHEN_IN_COMMENT",message)
    p.advance(1)
   }
   p.advance(1);continue
  }
  p.commentComplex(data,size);return
 }
}
// Once the ASCII accelerator transfers here, q and r are consumed lookahead:
// diagnostics belong to the current cursor, not the first retained hyphen.
func (p *parser) commentComplex(data,size int) {
 committed:=p.i
 q,ql:=p.currentNormalizedCharacter()
 if p.err!=nil{return}
 if q==0 {p.event("XML_ERR_COMMENT_NOT_FINISHED","Comment not terminated");return}
 if !xmlChar(q) {p.event("XML_ERR_INVALID_CHAR",fmt.Sprintf("xmlParseComment: invalid xmlChar value %d",q));return}
 ql=p.advanceCharacter(q,ql);qEnd:=p.i
 r,rl:=p.currentNormalizedCharacter()
 if p.err!=nil{return}
 if r==0 {p.event("XML_ERR_COMMENT_NOT_FINISHED","Comment not terminated");return}
 // The release's second-lookahead branch deliberately reports retained q.
 if !xmlChar(r) {p.event("XML_ERR_INVALID_CHAR",fmt.Sprintf("xmlParseComment: invalid xmlChar value %d",q));return}
 rl=p.advanceCharacter(r,rl);rEnd:=p.i
 initial:=true
 cur,l:=p.currentNormalizedCharacter()
 for p.poll() {
  if p.err!=nil {return}
  if cur==0 {
   message:="Comment not terminated"
   if !initial {message+=" \n<!--"+strings.TrimRight(p.commentPreview(data,committed),"\n")}
   p.event("XML_ERR_COMMENT_NOT_FINISHED",message);return
  }
  if !xmlChar(cur) {p.event("XML_ERR_INVALID_CHAR",fmt.Sprintf("xmlParseComment: invalid xmlChar value %d",cur));return}
  if q=='-'&&r=='-' {
   if cur=='>' {p.advance(1);return}
   p.event("XML_ERR_HYPHEN_IN_COMMENT","Comment must not contain '--' (double-hyphen)")
  }
  size+=ql;committed=qEnd
  q,ql,qEnd=r,rl,rEnd
  r=cur;rl=p.advanceCharacter(cur,l);rEnd=p.i
  cur,l=p.currentNormalizedCharacter()
  initial=false
  // The limit counts committed payload, excluding the two retained characters,
  // and is checked after NEXTL at the new reporting cursor.
  if size>maxText {p.event("XML_ERR_COMMENT_NOT_FINISHED","Comment too big found");return}
 }
}
func (p *parser) commentPreview(start,end int) string {
 prefix:=p.s[start:min(end,start+50)]
 if !strings.ContainsRune(prefix,'\r') {return prefix}
 var b strings.Builder
 for i:=start;i<end&&b.Len()<50;i++ {
  if p.s[i]=='\r' {
   b.WriteByte('\n')
   if i+1<end&&p.s[i+1]=='\n' {i++}
  }else{b.WriteByte(p.s[i])}
 }
 return b.String()
}
func (p *parser) pi() {
 start,id:=p.i,p.inputID
 p.advance(2);target:=p.name()
 dataStart:=p.i
 defer func(){
  if p.err==nil&&id==p.inputID&&p.i>=start+4&&p.s[p.i-2:p.i]=="?>" {
   if p.tree==nil{if !p.inDTD{p.noteKind(piNode)};return}
   p.collectMarkup(piNode,target,dataStart,p.i-2,start)
  }
 }()
 if p.err!=nil{return}
 if target=="" {p.event("XML_ERR_PI_NOT_STARTED","xmlParsePI : no target name");return}
 p.checkPITarget(target)
 if p.has("?>") {p.advance(2);return}
 if !p.spaces() {p.event("XML_ERR_SPACE_REQUIRED",p.joinMessage("ParsePI: PI ",target," space expected"))}
 dataStart=p.i
 size:=0
 catalog:=target=="oasis-xml-catalog"&&p.effective==Document&&p.depth==0&&!p.rootSeen&&!p.inDTD
 data:=namespaceValue{capture:true,construction:true,start:p.i,end:p.i}
 readStart:=p.i
 r,n:=p.currentNormalizedCharacter()
 for p.i<len(p.s)&&p.poll() {
  if p.has("?>") {
   p.advance(2)
   if catalog {
    value:=p.s[data.start:data.end];if data.transformed {value=data.uri.String()}
    p.catalogPI(value)
   }
   return
  }
  if n==0||!xmlChar(r) {break}
  size+=p.advanceCharacter(r,n)
  end:=p.i
  if catalog {p.namespaceAppend(&data,r,readStart,end)}
  readStart=p.i;r,n=p.currentNormalizedCharacter()
  if p.err!=nil{return}
  if size>maxText {p.event("XML_ERR_PI_NOT_FINISHED",p.joinMessage("PI ",target," too big found"));return}
 }
 if p.err==nil {p.event("XML_ERR_PI_NOT_FINISHED",p.joinMessage("ParsePI: PI ",target," never end ..."))}
}
// Only the document prolog admits catalog PI syntax warnings. Validation never
// opens the named catalog: external entity resolution belongs to the DTD owner.
func (p *parser) catalogPI(value string) {
 p.err=p.ctx.Err();if p.err!=nil{return}
 at:=0
 for at<len(value)&&space(value[at]) {
  if at&4095==0 {p.err=p.ctx.Err();if p.err!=nil{return}};at++
 }
 valid:=strings.HasPrefix(value[at:],"catalog")
 if valid {
  at+=7
  for at<len(value)&&space(value[at]) {
   if at&4095==0 {p.err=p.ctx.Err();if p.err!=nil{return}};at++
  }
  // The source silently ignores a missing equals sign after "catalog".
  if at==len(value)||value[at]!='=' {return}
  at++
  for at<len(value)&&space(value[at]) {
   if at&4095==0 {p.err=p.ctx.Err();if p.err!=nil{return}};at++
  }
  valid=at<len(value)&&(value[at]=='\''||value[at]=='"')
  if valid {
   quote:=value[at];at++
   for at<len(value)&&value[at]!=quote {
    if at&4095==0 {p.err=p.ctx.Err();if p.err!=nil{return}};at++
   }
   valid=at<len(value)
   if valid {
    at++
    for at<len(value)&&space(value[at]) {
     if at&4095==0 {p.err=p.ctx.Err();if p.err!=nil{return}};at++
    }
    valid=at==len(value)
   }
  }
 }
 if !valid {
  end:=len(value)
  for end>0&&value[end-1]=='\n' {
   if (len(value)-end)&4095==0 {p.err=p.ctx.Err();if p.err!=nil{return}};end--
  }
  p.eventLevel("XML_WAR_CATALOG_PI",p.joinMessage("Catalog PI syntax error: ",value[:end]),Warning)
 }
}
func (p *parser) cdata() {
 start,id:=p.i,p.inputID
 defer func(){if p.err==nil&&id==p.inputID&&p.i>=start+12&&p.s[p.i-3:p.i]=="]]>"{p.collectValue(cdataNode,p.s[start+9:p.i-3])}}()
 p.advance(9)
 data:=p.i;singleByte:=p.failedSingleByte
 r,rl:=p.currentNormalizedCharacter()
 if p.err!=nil{return}
 if !xmlChar(r) {p.event("XML_ERR_CDATA_NOT_FINISHED","Unregistered error message");return}
 rl=p.advanceCharacter(r,rl);rEnd:=p.i
 s,sl:=p.currentNormalizedCharacter()
 if p.err!=nil{return}
 if !xmlChar(s) {p.event("XML_ERR_CDATA_NOT_FINISHED","Unregistered error message");return}
 sl=p.advanceCharacter(s,sl);sEnd:=p.i
 size:=0;committed:=data
 cur,l:=p.currentNormalizedCharacter()
 for p.poll() {
  if !xmlChar(cur) {
   // The release restores CONTENT here. Leave the current scalar unconsumed,
   // and format only the committed payload, excluding both retained characters.
   message:=strings.TrimRight("CData section not finished\n"+p.cdataPreview(data,committed,singleByte),"\n")
   p.event("XML_ERR_CDATA_NOT_FINISHED",message);return
  }
  if r==']'&&s==']'&&cur=='>' {p.advanceCharacter(cur,l);p.cdataStale=false;return}
  size+=rl;committed=rEnd
  r,rl,rEnd=s,sl,sEnd
  s=cur;sl=p.advanceCharacter(cur,l);sEnd=p.i
  cur,l=p.currentNormalizedCharacter()
  // The pinned Debian limit counts committed payload after NEXTL, excluding
  // the two consumed lookahead characters. Overlength alone retains CDATA.
  if size>maxText {
   p.event("XML_ERR_CDATA_NOT_FINISHED","CData section too big found")
   p.cdataStale=true;return
  }
 }
}

// Reconstruct at most 50 committed payload bytes only after failure. Validation
// retains source endpoints rather than allocating the release's CDATA buffer.
func (p *parser) cdataPreview(start,end int,singleByte bool) string {
 var b strings.Builder
 for i:=start;i<end&&b.Len()<50; {
  r,n:=decodeUTF8(p.s[i:end])
  if singleByte||n==0 {singleByte=true;r=rune(p.s[i]);n=1}
  i+=n
  if r=='\r' {r='\n';if i<end&&p.s[i]=='\n'{i++}}
  if n==1 {b.WriteByte(byte(r));continue}
  var encoded [4]byte
  width:=utf8.EncodeRune(encoded[:],r)
  b.Write(encoded[:min(width,50-b.Len())])
 }
 return b.String()
}

// After CDATA overlength, the ASCII CharData accelerator cannot commit ordinary
// text callbacks in stale CDATA state. Its no-progress error differs from an
// initial lone CR/non-ASCII transfer to complex CharData, which consumes normally.
func (p *parser) failedCDataText() {
 start:=p.i
 if p.i==len(p.s)||p.byte()==0{return}
 complex:=p.byte()>=128||p.byte()=='\r'&&(len(p.s)-p.i==1||p.s[p.i+1]!='\n')
 for (p.byte()==' '||p.byte()=='\n')&&p.poll(){p.advance(1)}
 if p.byte()=='<'{return} // this special blank path commits even in CDATA state
 for p.i<len(p.s)&&p.byte()!=0&&p.poll() {
  if p.has("]]>") {
   at:=p.i;if !complex {p.i=start}
   p.event("XML_ERR_MISPLACED_CDATA_END","Sequence ']]>' not allowed in content")
   if complex {p.advance(1);continue}
   p.i=at+1;return // ASCII assigns cur=in+1 without incrementing column
  }
  if p.byte()=='<'||p.byte()=='&' {break}
  if p.character()==0 {p.stopped=true;return}
 }
 if complex||p.err!=nil||p.i==start&&p.i==len(p.s){return}
 p.i=start
 p.event("XML_ERR_INTERNAL_ERROR","internal error: detected an error in element content")
 p.stopped=true
}
