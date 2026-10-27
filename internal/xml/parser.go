package xml

func (p *parser) top() {
 if p.stopped||p.err!=nil {return}
 if p.has("\xff\xfe") || p.has("\xfe\xff") {
  p.event("XML_ERR_INTERNAL_ERROR","internal error: detected an error in element content");return
 }
 if p.effective==Document {
  if len(p.s)==0 {p.event("XML_ERR_INTERNAL_ERROR","switching encoding : no input");p.event("XML_ERR_DOCUMENT_EMPTY","Document is empty");return}
  p.misc()
  if p.err!=nil||p.stopped{return}
  if p.has("<!DOCTYPE") {p.dtd();p.misc()}
  if p.err!=nil||p.stopped{return}
  if p.i==len(p.s) {p.event("XML_ERR_DOCUMENT_EMPTY","Start tag expected, '<' not found");return}
  if !p.has("<")||p.has("</")||p.has("<!")&&p.dtdState==nil {p.event("XML_ERR_DOCUMENT_EMPTY","Start tag expected, '<' not found");return}
  p.element();p.rootSeen=true
  if p.stopped||p.err!=nil{return}
  p.misc()
  if p.i<len(p.s)&&p.err==nil {p.event("XML_ERR_DOCUMENT_END","Extra content at the end of the document")}
  return
 }
 for p.i<len(p.s)&&p.poll()&&!p.stopped {
  // Content's RAW==0 boundary ends continuation of an already-failed parse.
  // Do not reinterpret that terminator as CharData after a production returns;
  // clean input still requires complete-byte validation, not prefix acceptance.
  if p.byte()==0&&p.illFormed {return}
  before:=p.i
  switch {
  case p.has("<!DOCTYPE")&&p.illFormed:p.stopped=true;return
  case p.has("<!--"):p.comment()
  case p.has("<?"):p.pi()
  case p.has("<![CDATA["):p.cdata();p.rootSeen=true
  case p.has("</"):
   p.event("XML_ERR_NOT_WELL_BALANCED","chunk is not well balanced");p.stopped=true
  case p.has("<"):p.element();p.rootSeen=true
  default:p.text()
  }
  if !p.contentBoundary(){return}
  if p.i==before {return}
 }
}
func (p *parser) misc() {
 for p.i<len(p.s)&&p.poll()&&!p.stopped {
  p.spaces()
  if p.has("<!--") {p.comment()}else if p.has("<?") {p.pi()}else{return}
 }
}
func (p *parser) text() {
 if p.cdataStale {
  if p.byte()=='&' {p.contentReference()}else{p.failedCDataText()}
  return
 }
 start:=p.i;ascii:=true
 collectStart:=p.i
 defer func(){if p.i>collectStart{p.collectValue(textNode,p.s[collectStart:p.i])}}()
 for p.i<len(p.s)&&p.byte()!='<'&&p.poll() {
  if p.depth==0&&!space(p.byte()) {p.rootSeen=true}
  if p.has("]]>") {
   at:=p.i
   if ascii {p.i=start}
   p.event("XML_ERR_MISPLACED_CDATA_END","Sequence ']]>' not allowed in content")
   if ascii {p.i=at+1}else{p.advance(1)}
   start=p.i;ascii=true;continue
  }
  if p.byte()=='&' {
   if p.i!=start&&!p.contentBoundary(){return}
   if p.i>collectStart{p.collectValue(textNode,p.s[collectStart:p.i])}
   p.contentReference()
   collectStart=p.i
   if !p.contentBoundary(){return}
   start=p.i;ascii=true;continue
  }
  if p.byte()==0&&p.depth>0 {return}
  at:=p.i;crlf:=p.byte()=='\r'&&len(p.s)-p.i>1&&p.s[p.i+1]=='\n'
  if p.byte()>=128||p.byte()=='\r'&&!crlf {ascii=false}
  if p.character()==0 {
   if p.err==nil {p.event("XML_ERR_INTERNAL_ERROR","internal error: detected an error in element content")}
   p.stopped=true;return
  }
  // The ASCII accelerator commits the LF cursor before its CRLF restart.
  if ascii&&crlf {start=at+1}
 }
}
