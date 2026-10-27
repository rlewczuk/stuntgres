package xml

import (
 "errors"
 "strings"
 "testing"
)

func TestXMLCoreLimits(t *testing.T) {
 for _,m:=range []Mode{Content,Document} {
  accept(t,"<a>"+strings.Repeat("x",10000001)+"</a>",m)
  accept(t,"<"+strings.Repeat("a",50000)+"/>",m)
  e:=reject(t,"<"+strings.Repeat("a",50001)+"/>",m)
  messages:=[]string{"Name too long: NCName","StartTag: invalid element name"}
  if m==Document{messages=append(messages,"Extra content at the end of the document")}
  if len(e.Diagnostics)!=len(messages){t.Fatal("name limit recovery",e.Diagnostics)}
  for i,msg:=range messages{if e.Diagnostics[i].Message!=msg{t.Fatal(e.Diagnostics)}}
  // Source-derived NCName accelerator cursor, distinct from scan distance.
  for _,d:=range e.Diagnostics {
   if d.Offset!=1||d.Column!=2||d.SourceContext!="<"+strings.Repeat("a",79)||d.Caret!=" ^"{t.Fatal(d)}
  }
  depth:=256;if m==Document{depth++}
  accept(t,strings.Repeat("<a>",depth)+strings.Repeat("</a>",depth),m)
  e=reject(t,strings.Repeat("<a>",depth+1)+strings.Repeat("</a>",depth+1),m)
  if len(e.Diagnostics)!=1||e.Diagnostics[0].Message!="Excessive depth in document: 256 use XML_PARSE_HUGE option"{t.Fatal(e.Diagnostics)}
  // Source-derived guard placement: existing depth is checked before '<'.
  d:=e.Diagnostics[0]
  if d.Offset!=3*depth||d.Column!=3*depth+1||d.SourceContext!="a>"+strings.Repeat("<a>",26)||d.Caret!=strings.Repeat(" ",79)+"^"{t.Fatal(d)}
  for _,tc:=range []struct{open,close string;messages []string;source string;caret int}{
   {"<a><!--","--></a>",[]string{"Comment too big found"},"<a><!--"+strings.Repeat("x",73),7},
   {"<a><?go ","?></a>",[]string{"PI go too big found"},strings.Repeat("x",80),79},
   {"<a><![CDATA[","]]></a>",[]string{"CData section too big found","internal error: detected an error in element content"},strings.Repeat("x",78)+"]]",79},
  } {
   accept(t,tc.open+strings.Repeat("x",10000000)+tc.close,m)
   e=reject(t,tc.open+strings.Repeat("x",10000001)+tc.close,m)
   if e.Detail()!=blocks(1,tc.source,tc.caret,tc.messages...){t.Fatalf("construct %q: %q",tc.open,e.Detail())}
   // Source-derived structured column: the comment accelerator has advanced
   // col while retaining the initial payload byte cursor for this diagnostic.
   if tc.open=="<a><!--"&&e.Diagnostics[0].Column!=10000009 {t.Fatal(e.Diagnostics[0])}
  }
  e=reject(t,"<a><![CDATA["+strings.Repeat("x",16777216)+"]]></a>",m)
  want:=blocks(1,strings.Repeat("x",80),79,"CData section too big found","Sequence ']]>' not allowed in content")+"\n"+blocks(1,strings.Repeat("x",79)+"]",79,"internal error: detected an error in element content")
  if e.Detail()!=want{t.Fatalf("CDATA recovery %q",e.Detail())}
  // Source-derived sliding-lookahead failure and stale-state cursor commits,
  // independent of the payload's total length or buffer allocation capacity.
  if e.Diagnostics[0].Offset!=10000015||e.Diagnostics[1].Offset!=10000015||e.Diagnostics[2].Offset!=16777229{t.Fatal(e.Diagnostics)}
 }
 // Source-derived SHRINK counterexample: total input >10M is not a cap.
 accept(t,strings.Repeat("x",10000001)+`<a x="s"/>`,Content)
 accept(t,"<a>"+strings.Repeat("x",10000001)+"&amp;<b x='s'/></a>",Content)
 for _,n:=range []int{9999991,9999992,10000000,10000001} {
  for _,m:=range []Mode{Content,Document} {
   input:=`<a x="`+strings.Repeat("x",n)+`"/>`
   if n<=10000000&&(m==Document||n<=9999991){accept(t,input,m);continue}
   e:=reject(t,input,m)
   if n<=10000000 {
    want:=blocks(1,strings.Repeat("x",77)+`"/>`,79,"internal error: Huge input lookup")
    if e.Detail()!=want {t.Fatalf("fragment input window %d: %q",n,e.Detail())}
   }else{
    messages:=[]string{"AttValue length too long","attributes construct error","Couldn't find end of Start Tag a line 1"}
    if m==Document{messages=append(messages,"Extra content at the end of the document")}
    if e.Detail()!=blocks(1,`<a x="`+strings.Repeat("x",74),5,messages...){t.Fatalf("attribute length %d: %q",m,e.Detail())}
   }
  }
 }
 // Release numeric guards only; no gigabyte allocations or OOM observation.
 huge:=int(^uint(0)>>1)/4
 if !conversionInputSize(huge-1)||conversionInputSize(huge)||conversionInputSize(-1){t.Fatal("huge conversion guard")}
 for _,tc:=range []struct{current,extra int;ok bool}{{0,0,true},{0,0x3ffffffe,true},{0,0x3fffffff,false},{7,0x3ffffff7,true},{7,0x3ffffff8,false},{-1,0,false},{1,-1,false}} {if appendSize(tc.current,tc.extra)!=tc.ok{t.Fatal("append guard",tc)}}
 var se *SizeError
 if e:=conversionSizeError(huge);!errors.As(e,&se)||se.Kind!=ConversionSize||se.Message!="out of memory"||se.SourceLength!=huge{t.Fatal(e)}
 if e:=conversionSizeError(123);!errors.As(e,&se)||se.Detail!="String of 123 bytes is too long for encoding conversion."{t.Fatal(e)}
 if e:=appendSizeError(10,20);!errors.As(e,&se)||se.Kind!=AppendSize||se.Message!="string buffer exceeds maximum allowed length (1073741823 bytes)"||se.Detail!="Cannot enlarge string buffer containing 10 bytes by 20 more bytes."{t.Fatal(e)}
}
