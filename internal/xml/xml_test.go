package xml

import (
 "context"
 "errors"
 "strings"
 "testing"
)

// Literal grammar and diagnostics were captured before implementation from
// PostgreSQL18.6 UTF8/C, Debian libxml2 2.9.14+dfsg-1.3~deb12u6. See data/README.
func accept(t *testing.T,s string,m Mode) Result {
 t.Helper();r,e:=Parse(context.Background(),s,Options{Mode:m})
 if e!=nil {t.Fatalf("accept %q mode%d: %v",short(s),m,e)}
 if !r.valid||r.Original()!=s||r.RequestedMode()!=m||r.EffectiveMode()!=m {t.Fatal("result spelling/mode/validity")};return r
}
func reject(t *testing.T,s string,m Mode)*ParseError {
 t.Helper();r,e:=Parse(context.Background(),s,Options{Mode:m});var p *ParseError
 if !errors.As(e,&p)||r.valid||r.Original()!="" {t.Fatalf("reject %q mode%d: result=%+v error=%v",short(s),m,r,e)}
 if p.RequestedMode!=m||p.EffectiveMode!=m {t.Fatal("lost requested/effective mode")};return p
}
func short(s string) string {if len(s)>120{return s[:120]+"..."};return s}
func TestXMLCoreGrammar(t *testing.T) {
 for _,s:=range []string{
  `<é_漢𐀀 a·b="&quot;&apos;&lt;&gt;&amp;&#233;&#xE9;">é</é_漢𐀀>`,
  `<!--p--><?go a?><a><![CDATA[<&]]></a><!--e--><?end?>`,
  `<a/>`,`<a x = 'v' y="&#x20;">text&amp;<b/><!-- --></a>`,
 } {for _,m:=range []Mode{Content,Document}{accept(t,s,m)}}
 for _,s:=range []string{`<a>`,`<a/><`,`<a></b>`,`</a>`,`<a x/>`,`<a x=1/>`,`<a x="1"y="2"/>`,`<a x="<"/>`,`<a>&bad;</a>`,`<a>&amp</a>`,`<a>&;</a>`,`<a>&#;</a>`,`<a>&#X41;</a>`,`<a>]]></a>`,`<a><!--x--x--></a>`,`<a><!--x</a>`,`<a><![CDATA[x</a>`,`<a><?go x</a>`,`<a><?XmL hi?></a>`,`<!wrong><a/>`,`<a>\x00</a>`} {
  if s==`<a>\x00</a>` {s="<a>\x00</a>"}
  for _,m:=range []Mode{Content,Document}{reject(t,s,m)}
 }
}
func TestXMLCoreNamesCharacters(t *testing.T) {
 for _,name:=range []string{"_a",":a","é","漢","𐀀",string(rune(0xEFFFF)),"a·b","a\u0300"} {accept(t,"<"+name+"/>",Document)}
 for _,name:=range []string{"1a","-a","\u00B7a","\u037E",string(rune(0xF0000))} {reject(t,"<"+name+"/>",Content)}
 for _,s:=range []string{"<a>\x00</a>","<a>\x01</a>","<a>\xff</a>","<a>\xc0\xaf</a>","<a>\xed\xa0\x80</a>","<a>\xf4\x90\x80\x80</a>","<a>\uFFFE</a>","<a\u00A0x='1'/>"} {for _,m:=range []Mode{Content,Document}{reject(t,s,m)}}
 accept(t,"<a>\t\r\n\uD7FF\uE000\uFFFD\U0010FFFF</a>",Document)
}
func TestXMLCoreModes(t *testing.T) {
 for _,s:=range []string{"","abc","<a/><b/>","<![CDATA[<&]]>","<a/>junk"} {accept(t,s,Content);reject(t,s,Document)}
 for _,s:=range []string{"<a/><","<a/>junk<"} {for _,m:=range []Mode{Content,Document}{reject(t,s,m)}}
 for _,s:=range []string{"  <!--p--><?go?><a/>\n<!--e-->","<?xml version='1.0'?><a/>"} {for _,m:=range []Mode{Content,Document}{accept(t,s,m)}}
 if _,e:=Parse(context.Background(),"",Options{Mode:2});e==nil {t.Fatal("invalid mode")}
 if _,e:=Parse(context.Background(),"",Options{InputKind:2});e==nil {t.Fatal("invalid input kind")}
 if out,w,e:=OutputText(context.Background(),Result{});e==nil||out!=""||w!=(OutputWarning{}) {t.Fatal("zero result output")}
}
func TestXMLCoreNamespaces(t *testing.T) {
 for _,s:=range []string{`<p:r/>`,`<a xmlns="relative"/>`,`<a xmlns="&lt;"/>`,`<a xmlns:p="u" xmlns:q="u" p:x="1" q:x="2"/>`} {for _,m:=range []Mode{Content,Document}{accept(t,s,m)}}
 for _,s:=range []string{`<a x="1" x="2"/>`,`<a p:x="1" p:x="2"/>`,`<a xmlns:p="u" xmlns:p="v"/>`} {for _,m:=range []Mode{Content,Document}{e:=reject(t,s,m);if len(e.Diagnostics)!=1||e.Diagnostics[0].Condition!="XML_ERR_ATTRIBUTE_REDEFINED" {t.Fatalf("lexical duplicate: %+v",e)}}}
 // Source-derived QName recovery (matching parser.c8779-8846), not a
 // namespace-binding validator or a generic XML Name shortcut.
 for _,m:=range []Mode{Content,Document} {
  for _,s:=range []string{`<a:1/>`,`<a:1:2/>`,`<a:b:c/>`,`<a:1></a:1>`}{accept(t,s,m)}
  reject(t,`<a:b:1/>`,m)
  prefix,local:=strings.Repeat("p",50000),strings.Repeat("l",50000)
  accept(t,"<"+prefix+":"+local+"/>",m)
  reject(t,"<"+prefix+":"+local+"x/>",m)
  reject(t,`<a p:1="1" p:1="2"/>`,m)
 }
}
func TestXMLCoreDTDRecognition(t *testing.T) {
 for _,s:=range []string{`<!DOCTYPE a><a/>`,`<!DOCTYPE a [<!ENTITY x "v">]><a>&x;</a>`,`<!DOCTYPE a SYSTEM "file:///never-open"><a/>`,`<?xml version='1.0'?> <!--p--><?go?><!DOCTYPE a><a/>`} {
  for _,m:=range []Mode{Content,Document} {
   r,e:=Parse(context.Background(),s,Options{Mode:m})
   if e!=nil||!r.valid||r.RequestedMode()!=m||r.EffectiveMode()!=Document||r.Original()!=s{t.Fatal(r,e)}
  }
 }
 for _,s:=range []string{`<!-- <!DOCTYPE a> --><a/>`,`<a><![CDATA[<!DOCTYPE a>]]>&lt;!DOCTYPE a&gt;</a>`} {accept(t,s,Document)}
 // Invalid leading Misc is a grammar failure, not effective-mode promotion.
 e:=reject(t,`<!--x--x--><!DOCTYPE a><a/>`,Content)
 if e.EffectiveMode!=Content||len(e.Diagnostics)!=1||e.Diagnostics[0].Condition!="XML_ERR_HYPHEN_IN_COMMENT"{t.Fatal(e)}
}
func TestXMLCoreFeatureSmoke(t *testing.T) {
 accept(t,`<!--p--><?go?><é><![CDATA[<&]]></é>`,Document)
 for _,s:=range []string{"","text","<a/><b/>"}{accept(t,s,Content);reject(t,s,Document)}
 accept(t,`<p:r/>`,Document);reject(t,`<a x="1" x="2"/>`,Document)
 s:=`<?xml version="1.0" encoding="ISO-8859-1"?><a>é</a>`
 r:=accept(t,s,Document)
 out,w,e:=OutputText(context.Background(),r);if e!=nil||w!=(OutputWarning{})||out!="<a>é</a>"{t.Fatal(out,w,e)}
 r,e=Parse(context.Background(),strings.Replace(s,"é","\xe9",1),Options{InputKind:Binary});if e!=nil||r.Original()!=s{t.Fatal(r,e)}
 out,w,e=OutputBinary(context.Background(),r,8);if e!=nil||w!=(OutputWarning{})||out!="<?xml version=\"1.0\" encoding=\"LATIN1\"?><a>\xe9</a>"{t.Fatal(out,w,e)}
 reject(t,"<a>\xe9</a>",Content)
 r=accept(t,`<?xml version="1.1"?><a/>`,Document);if len(r.Diagnostics())!=1||r.Diagnostics()[0].Severity!=Warning{t.Fatal("version warning")}
 e2:=reject(t,`<twoerrors>&idontexist;</unbalanced>`,Content)
 if e2.Detail()!="line 1: Entity 'idontexist' not defined\n<twoerrors>&idontexist;</unbalanced>\n                       ^\nline 1: Opening and ending tag mismatch: twoerrors line 1 and unbalanced\n<twoerrors>&idontexist;</unbalanced>\n                                    ^"{t.Fatal(e2.Detail())}
 ctx,cancel:=context.WithCancel(context.Background());cancel();r,e=Parse(ctx,"<a/>",Options{});if !errors.Is(e,context.Canceled)||r.valid{t.Fatal(r,e)}
 accept(t,"<a/>",Content)
 r,e=Parse(context.Background(),"<!DOCTYPE a><a/>",Options{});if e!=nil||r.EffectiveMode()!=Document{t.Fatal(r,e)}
}
