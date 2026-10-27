package xml

import (
 "context"
 "fmt"
 "strings"
 "unicode/utf8"
)

const maxName = 50000
const maxText = 10000000

type parser struct {
 ctx context.Context
 s string
 i, base, line, column, nextPoll int
 requested, effective Mode
 declaration Declaration
 events []Diagnostic
 err error
 depth int
 rootSeen bool
 stopped bool
 inputEOF bool
 haltedInput bool
 diagnosticBytes int
 illFormed bool
 cdataStale bool
 fragmentInput bool
 windowBase int
 failedSingleByte bool
 namespaceBindings map[string]int
 namespaceHashes map[uint64]int
 namespaceURIs []namespaceURI
}
func (p *parser) poll() bool {
 if p.err!=nil { return false }
 if p.i>=p.nextPoll { p.err=p.ctx.Err(); p.nextPoll=p.i+min(4096,len(p.s)-p.i) }
 return p.err==nil
}
func (p *parser) has(s string) bool {if p.haltedInput {return s==""};return strings.HasPrefix(p.s[p.i:],s)}
func (p *parser) byte() byte { if p.haltedInput||p.i==len(p.s) { return 0 }; return p.s[p.i] }
func space(b byte) bool { return b==' '||b=='\t'||b=='\r'||b=='\n' }
func (p *parser) advance(n int) {
 end:=p.i+n
 for p.i<end && p.poll() {
  r,w:=utf8.DecodeRuneInString(p.s[p.i:end]); if r=='\n' { p.line++;p.column=1 } else { p.column++ };p.i+=w
 }
}
func (p *parser) spaces() bool {
 start:=p.i
 for p.i<len(p.s)&&space(p.byte())&&p.poll(){p.advance(1)}
 if p.i!=start {p.contentBoundary()}
 return p.i!=start
}
func xmlChar(r rune) bool { return r==9||r==10||r==13||r>=0x20&&r<=0xD7FF||r>=0xE000&&r<=0xFFFD||r>=0x10000&&r<=0x10FFFF }
func nameStart(r rune) bool {
 return r==':'||r=='_'||r>='A'&&r<='Z'||r>='a'&&r<='z'||r>=0xC0&&r<=0xD6||r>=0xD8&&r<=0xF6||r>=0xF8&&r<=0x2FF||r>=0x370&&r<=0x37D||r>=0x37F&&r<=0x1FFF||r>=0x200C&&r<=0x200D||r>=0x2070&&r<=0x218F||r>=0x2C00&&r<=0x2FEF||r>=0x3001&&r<=0xD7FF||r>=0xF900&&r<=0xFDCF||r>=0xFDF0&&r<=0xFFFD||r>=0x10000&&r<=0xEFFFF
}
func nameChar(r rune) bool { return nameStart(r)||r=='-'||r=='.'||r>='0'&&r<='9'||r==0xB7||r>=0x300&&r<=0x36F||r>=0x203F&&r<=0x2040 }
func (p *parser) name() string {name,_:=p.scanName(true,false,"Name");return name}
func (p *parser) scanName(colon,token bool,label string)(string,bool) {
 start,line,col:=p.i,p.line,p.column;end:=start
 // The raw accelerator requires an actual initial ASCII NameStart. Invalid
 // ASCII starts still perform the complex production's reporting read.
 initial:=p.byte()
 fast:=!token&&initial<128&&nameStart(rune(initial))&&(colon||initial!=':')
 for p.i<len(p.s)&&p.poll() {
  // Even a non-name non-ASCII terminator selects the complex production.
  if p.byte()>=128{fast=false}
  var r rune
  var n int
  if fast {r,n=p.currentCharacter()}else{r,n=p.currentNormalizedCharacter()}
  if n==0||r==':'&&!colon||p.i==start&&!token&&!nameStart(r)||!nameChar(r){break}
  p.advanceCharacter(r,n);end=p.i
  // NmToken has no ASCII accelerator and checks while constructing its token.
  if token&&end-start>maxName {
   p.currentNormalizedCharacter()
   if p.err!=nil{return "",false}
   p.event("XML_ERR_NAME_TOO_LONG","Name too long: "+label);return "",true
  }
 }
 if p.err!=nil{return "",false}
 if end-start>maxName {
  // Name/NCName accelerators commit only after a positive ASCII terminator.
  // Complex names, including terminal input, report at their consumed cursor.
  if fast&&p.byte()>0&&p.byte()<128 {
   p.i,p.line,p.column=start,line,col
   p.nextPoll=min(p.nextPoll,p.i)
  }
  p.event("XML_ERR_NAME_TOO_LONG","Name too long: "+label);return "",true
 }
 // Complex CUR_CHAR can skip CR at the terminator. The reporting cursor
 // moves, but the returned spelling and counted length exclude that CR.
 return p.s[start:end],false
}

// QName recovery is distinct from Name. Namespace-domain failures themselves
// are ignored for wellformedness, but unconsumed bytes still reach the grammar.
// Return lexical spelling for identity and the source-local spelling for
// diagnostic consumers. Recovery can return an unprefixed name containing ':'.
func (p *parser) qname() (string,string) {
 start:=p.i
 first,_:=p.scanName(false,false,"NCName")
 if p.err!=nil||p.stopped{return "",""}
 // A NULL initial NCName still permits an ordinary Name beginning at ':'.
 if first=="" {
  if p.byte()==':' {name:=p.name();return name,name}
  return "",""
 }
 if p.byte()!=':' {return first,first}
 p.advance(1);localStart:=p.i
 second,_:=p.scanName(false,false,"NCName")
 if p.err!=nil||p.stopped{return "",""}
 if second=="" {
  tokenStart:=p.i
  token,_:=p.scanName(true,true,"NmToken")
  if p.err!=nil||p.stopped{return "",""}
  // Failed NmToken retains the prefix and colon, not its consumed payload.
  // This recovery returns an unprefixed name, including its original colon.
  end:=tokenStart+len(token);if token=="" {end=localStart}
  // A consumed failed local component can separate the surviving prefix and
  // token. Assemble only those successful pieces, with checked/polled copies.
  if token!=""&&tokenStart!=localStart {
   name:=p.joinMessage(first,":",token);return name,name
  }
  name:=p.s[start:end];return name,name
 }
 end:=localStart+len(second)
 if p.byte()==':' {
  p.advance(1);suffixStart:=p.i
  suffix,_:=p.scanName(true,false,"Name")
  if p.err!=nil||p.stopped{return "",""}
  // NULL Name retains both successful components and the second colon.
  // Physical input end alone is not parser EOF; exclude the failed suffix.
  if suffix=="" {return p.s[start:suffixStart],p.s[localStart:suffixStart]}
  end=suffixStart+len(suffix)
 }
 return p.s[start:end],p.s[localStart:end]
}

// endName ports NameAndCompare/QNameAndCompare, not a generic QName reparse.
// The accelerator commits matched bytes to the column only at '>' or whitespace.
// Failed comparisons leave the scanner untouched for the appropriate fallback.
func (p *parser) endName(name,local string) (string,bool) {
 if !p.fragmentGrow()||p.stopped||p.err!=nil{return "",false}
 if len(p.s)-p.i>=len(name)&&p.equalName(p.s[p.i:p.i+len(name)],name) {
  end:=p.i+len(name)
  if end<len(p.s)&&(p.s[end]=='>'||space(p.s[end])) {
   p.column+=len(name);p.i=end
   return local,true
  }
 }
 if p.err!=nil{return "",false}
 if len(name)==len(local) {
  end:=p.name()
  return end,end!=""&&p.equalName(end,name)
 }
 end,endLocal:=p.qname()
 return endLocal,end!=""&&p.equalName(end,name)&&len(endLocal)==len(local)
}

// Comparisons do not move the parser cursor, so ordinary cursor polling cannot
// bound them. Each native string comparison is at most one polling chunk.
func (p *parser) equalName(a,b string) bool {
 if len(a)!=len(b){return false}
 for at:=0;at<len(a); {
  p.err=p.ctx.Err();if p.err!=nil{return false}
  end:=at+min(4096,len(a)-at)
  if a[at:end]!=b[at:end]{return false}
  at=end
 }
 return true
}
// decodeUTF8 preserves the release decoder's computed value and encoded width
// for surrogate/out-of-range sequences. They are never valid XML characters.
// Zero width means malformed bytes, not an XML-invalid decoded value.
func decodeUTF8(s string) (rune,int) {
 if len(s)==0{return 0,0}
 c:=s[0]
 if c<128{return rune(c),1}
 n:=2;value:=rune(c&0x1f);minimum:=rune(0x80)
 if c&0xe0==0xe0 {n=3;value=rune(c&0xf);minimum=0x800}
 if c&0xf0==0xf0 {n=4;value=rune(c&7);minimum=0x10000}
 if c&0xc0!=0xc0||c&0xf8==0xf8||len(s)<n{return 0,0}
 for j:=1;j<n;j++ {
  if s[j]&0xc0!=0x80{return 0,0}
  value=value<<6|rune(s[j]&0x3f)
 }
 if value<minimum{return 0,0}
 return value,n
}
// currentCharacter owns per-parse decoder failure state. A malformed sequence
// reports once and continues as single bytes only in an already-failed parse.
// Productions still own XML-invalid-scalar reporting and recovery.
func (p *parser) currentCharacter() (rune,int) {
 if !p.poll()||p.haltedInput||p.i==len(p.s) {return 0,0}
 if p.failedSingleByte {return rune(p.byte()),1}
 r,n:=decodeUTF8(p.s[p.i:])
 if n==0 {
  // A short terminal buffer is incomplete input, not a charset transition.
  // The enclosing production reports termination/no-progress at this cursor.
  if len(p.s)-p.i<4{return 0,0}
  var b strings.Builder
  b.WriteString("Input is not proper UTF-8, indicate encoding !\nBytes:")
  for j:=range min(4,len(p.s)-p.i) { fmt.Fprintf(&b," 0x%02X",p.s[p.i+j]) }
  p.event("XML_ERR_INVALID_CHAR",b.String())
  p.failedSingleByte=true
  return rune(p.byte()),1
 }
 if n>1&&!xmlChar(r) {
  p.event("XML_ERR_INVALID_CHAR",fmt.Sprintf("Char 0x%X out of allowed range",r))
 }
 return r,n
}
// currentNormalizedCharacter models the complex productions' CUR_CHAR read.
// A CRLF read moves only the raw cursor onto LF; cached NEXTL consumption then
// tracks that LF once. Lone CR returns LF but retains raw-byte column tracking.
// Failure-only single-byte decoding obeys the same normalization.
func (p *parser) currentNormalizedCharacter() (rune,int) {
 r,n:=p.currentCharacter()
 if r=='\r' {
  if p.i+1<len(p.s)&&p.s[p.i+1]=='\n' {p.i++}
  return '\n',n
 }
 return r,n
}
func (p *parser) advanceCharacter(r rune,n int) int {
 if r=='\r' {p.advance(1);if p.byte()=='\n' {p.advance(1)};return 1}
 // NEXTL advances one character even when Go rejects its encoded value.
 if p.byte()=='\n' {p.line++;p.column=1}else{p.column++}
 p.i+=n;return n
}
func (p *parser) character() int {
 r,n:=p.currentCharacter()
 if n==0 {return 0}
 if !xmlChar(r) {
  p.event("XML_ERR_INVALID_CHAR",fmt.Sprintf("PCDATA invalid Char value %d",r))
  // CharData's NEXTL skips the offending scalar before resuming content.
 }
 return p.advanceCharacter(r,n)
}
