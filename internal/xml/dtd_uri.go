package xml

// External identifiers are retained, never resolved. ENTITY additionally uses
// libxml2 uri.c's RFC3986 syntax check (including its permissive bracket host
// and IPv4-first token precedence). DOCTYPE identifiers do not use this check.
type uriScanner struct {p *parser;s string;i,nextPoll int}
func (u *uriScanner) byteAt(at int)byte {if at>=len(u.s){return 0};return u.s[at]}
func (u *uriScanner) current()byte{return u.byteAt(u.i)}
func (u *uriScanner) poll()bool {
 if u.p.err!=nil{return false}
 if u.i>=u.nextPoll{u.p.err=u.p.ctx.Err();u.nextPoll=u.i+min(4096,len(u.s)-u.i)}
 return u.p.err==nil
}
func uriAlpha(c byte)bool{return c>='a'&&c<='z'||c>='A'&&c<='Z'}
func uriDigit(c byte)bool{return c>='0'&&c<='9'}
func uriHex(c byte)bool{return uriDigit(c)||c>='a'&&c<='f'||c>='A'&&c<='F'}
func uriUnreserved(c byte)bool{return uriAlpha(c)||uriDigit(c)||c=='-'||c=='.'||c=='_'||c=='~'}
func uriSubDelimiter(c byte)bool{switch c{case '!','$','&','\'','(',')','*','+',',',';','=':return true};return false}
func (u *uriScanner) token(colon,at bool)bool {
 if !u.poll(){return false};c:=u.current()
 if uriUnreserved(c)||uriSubDelimiter(c)||colon&&c==':'||at&&c=='@'{u.i++;return true}
 if c=='%'&&uriHex(u.byteAt(u.i+1))&&uriHex(u.byteAt(u.i+2)){u.i+=3;return true}
 return false
}
func (u *uriScanner) decOctet(at int)(int,bool) {
 a,b,c:=u.byteAt(at),u.byteAt(at+1),u.byteAt(at+2)
 switch {
 case !uriDigit(a):return at,false
 case !uriDigit(b):return at+1,true
 case a!='0'&&!uriDigit(c):return at+2,true
 case a=='1'&&uriDigit(c):return at+3,true
 case a=='2'&&b>='0'&&b<='4'&&uriDigit(c):return at+3,true
 // This is the pinned uri.c comparison, not stricter host-address validation.
 case a=='2'&&b=='5'&&c>='0'&&b<='5':return at+3,true
 }
 return at,false
}
func (u *uriScanner) authority()bool {
 start:=u.i
 for u.token(true,false){}
 if u.p.err!=nil{return false}
 if u.current()=='@'{u.i++}else{u.i=start;u.nextPoll=start}
 host:=u.i
 if u.current()=='[' {
  u.i++
  for u.current()!=']'&&u.current()!=0&&u.poll(){u.i++}
  if u.current()!=']'||u.p.err!=nil{return false};u.i++
 }else{
  ipv4:=uriDigit(u.current());at:=u.i
  if ipv4{for part:=range 4{var ok bool;at,ok=u.decOctet(at);if !ok{ipv4=false;break};if part<3{if u.byteAt(at)!='.'{ipv4=false;break};at++}}}
  if ipv4{u.i=at}else{u.i=host;for u.token(false,false){}}
 }
 if u.p.err!=nil{return false}
 if u.current()==':' {
  u.i++;if !uriDigit(u.current()){return false};var port uint32
  for uriDigit(u.current())&&u.poll(){digit:=uint32(u.current()-'0');if port>(2147483647-digit)/10{return false};port=port*10+digit;u.i++}
 }
 return u.p.err==nil
}
func (p *parser) entityURI(s string)(valid,fragment bool) {
 u:=uriScanner{p:p,s:s};absolute:=false
 if uriAlpha(u.current()) {
  u.i++
  for (uriAlpha(u.current())||uriDigit(u.current())||u.current()=='+'||u.current()=='-'||u.current()=='.')&&u.poll(){u.i++}
  if u.current()==':'{absolute=true;u.i++}else{u.i=0;u.nextPoll=0}
 }
 authority:=u.current()=='/'&&u.byteAt(u.i+1)=='/'
 if authority {
  u.i+=2;if !u.authority(){return false,false}
  if c:=u.current();c!=0&&c!='/'&&c!='?'&&c!='#'{return false,false}
 }
 firstSegment:=!absolute&&!authority&&u.current()!='/'
 for u.poll() {
  if u.current()=='/'{firstSegment=false;u.i++;continue}
  if firstSegment&&u.current()==':'{return false,false}
  if !u.token(true,true){break}
 }
 if u.current()=='?' {
  u.i++
  for u.poll(){if u.current()=='/'||u.current()=='?'{u.i++;continue};if !u.token(true,true){break}}
 }
 if u.current()=='#' {
  fragment=true;u.i++
  for u.poll(){if c:=u.current();c=='/'||c=='?'||c=='['||c==']'{u.i++;continue};if !u.token(true,true){break}}
 }
 if p.err!=nil{return false,false}
 p.err=p.ctx.Err();return u.i==len(s)&&p.err==nil,fragment
}
