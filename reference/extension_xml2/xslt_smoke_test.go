package reference_test

import (
 "database/sql"
 "strings"
 "testing"

 "stuntgres"
 "stuntgres/reference/fixture"
)

func xsltSmokeStyle(output, declarations, body string) string {
 return `<xsl:stylesheet version="1.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">`+output+declarations+`<xsl:template match="/">`+body+`</xsl:template></xsl:stylesheet>`
}

func TestReferenceXML2XSLTFeatureSmoke(t *testing.T) {
 xml2Case(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  xml2Install(t,p,b,c)
  text:=`<xsl:output method="text" encoding="UTF-8"/>`
  bracket:=`<xsl:stylesheet version="1.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"><xsl:output method="text" encoding="UTF-8"/><xsl:template match="/"><xsl:apply-templates select="/doc/n"/></xsl:template><xsl:template match="n">[<xsl:value-of select="."/>]</xsl:template></xsl:stylesheet>`
  transform:=func(source,style,expected any){xml2Both(t,p,b,c,fixture.Select("SELECT xslt_process($1::text,$2::text) AS v",[]string{"v"},[]string{"TEXT"},[][]any{{expected}},source,style))}
  transform(`<doc><n>A</n><n>B</n></doc>`,bracket,"[A][B]")
  transform(`<doc><n>B</n><n>A</n></doc>`,bracket,"[B][A]")
  transform(`<doc><n>A</n><n>B</n></doc>`,xsltSmokeStyle(text,"",`<xsl:value-of select="/doc/n"/>`),"A")
  params:=xsltSmokeStyle(text,`<xsl:param name="p" select="'default'"/>`,`<xsl:value-of select="$p"/>`)
  transform("<r/>",params,"default")
  for _,v:=range []struct{parameters,expected string}{{"p='given'","given"},{"p=1+2","3"}}{xml2Both(t,p,b,c,fixture.Select("SELECT xslt_process($1::text,$2::text,$3::text) AS v",[]string{"v"},[]string{"TEXT"},[][]any{{v.expected}},"<r/>",params,v.parameters))}
  transform("<r/>",xsltSmokeStyle(text,"",""),"")
  transform(nil,bracket,nil)
  transform("<r/>",nil,nil)
  xml2Both(t,p,b,c,fixture.Select("SELECT xslt_process($1::text,$2::text,$3::text) AS v",[]string{"v"},[]string{"TEXT"},[][]any{{nil}},"<r/>",params,nil))
  transform("<r/>",xsltSmokeStyle("","",`<out>&lt;&amp;</out>`),"<?xml version=\"1.0\"?>\n<out>&lt;&amp;</out>\n")
  transform("<r/>",xsltSmokeStyle(text,"",`<xsl:text>&lt;&amp;</xsl:text>`),"<&")
  transform("<r>a<![CDATA[b]]>c</r>",xsltSmokeStyle(text,"",`<xsl:value-of select="count(/r/text())"/>:<xsl:value-of select="/r"/>`),"3:abc")
  transform("<r/>",xsltSmokeStyle(text,"",`<xsl:variable name="v"><a>A</a><b>B</b></xsl:variable><xsl:value-of select="string($v)"/>|<xsl:value-of select="boolean($v)"/>`),"AB|true")
  for _,v:=range []struct{label,payload,want string}{{"ISO-2022-JP","\x1b$B$\x22\x1b(B","あ"},{"ISO-2022-JP-2","\x1b$(D+1\x1b(B","é"},{"ISO-2022-KR","\x1b$)C\x0eGQ\x0f","한"},{"ISO-2022-CN","\x1b$)A\x0eVP\x0f","中"},{"UTF-7","+AOk-","é"}}{
   header:=`<?xml version="1.0" encoding="`+v.label+`"?>`
   transform(header+"<r>"+v.payload+"</r>",xsltSmokeStyle(text,"",`<xsl:value-of select="/r"/>`),v.want)
   transform("<r/>",header+xsltSmokeStyle(text,"",`<xsl:text>`+v.payload+`</xsl:text>`),v.want)
  }
  for _,v:=range []struct{source,style,params,code,message,detail string}{
   {"<r>",bracket,"","2200M","error parsing XML document","line 1: Premature end of data in tag r line 1\n<r>\n   ^"},
   {"<r/>","<bad/>","","10608","failed to parse stylesheet",""},
   {"<r/>",params,"p=","10608","failed to apply stylesheet","Invalid expression"},
  }{want:=xml2Error(v.code,v.message);want.Detail=v.detail;xml2Failure(t,p,b,c,"SELECT xslt_process($1::text,$2::text,$3::text)",want,false,v.source,v.style,v.params)}
  // Extend the oracle-first template witnesses with the complete staged,
  // no-I/O, datum and native-control traversal; do not replace those witnesses.
  transform("<r/>",`<t:transform xmlns:t="http://www.w3.org/1999/XSL/Transform" version="1.0"><t:output method="text"/><t:template match="/">ALT</t:template></t:transform>`,"ALT")
  transform("<r/>",`<xsl:stylesheet version="1.1" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">`+text+`<xsl:template match="/"><xsl:unknown><xsl:fallback>F</xsl:fallback></xsl:unknown></xsl:template></xsl:stylesheet>`,"F")
  transform(`<!DOCTYPE r [<!ENTITY i "I"><!ENTITY e SYSTEM "file:///stuntgres-xslt-no-such-file.xml">]><r>&i;&e;</r>`,xsltSmokeStyle(text,"",`<xsl:value-of select="r"/>`),"I")
  transform("<r/>",`<xsl:stylesheet version="1.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:d="urn:d">`+text+`<d:v>S</d:v><xsl:template match="/"><xsl:value-of select="document('')/*/d:v"/></xsl:template></xsl:stylesheet>`,"S")
  for _,v:=range []struct{source,style,stage,detail string}{
   {"<r/>","<xsl:stylesheet","style",xsltUnfinishedStyle},
   {"<r/>",xsltSmokeStyle(text,"",`<xsl:value-of select="["/>`),"compile","Invalid expression"},
   {"<r/>",xsltSmokeStyle(text,"",`<xsl:value-of select="$missing"/>`),"apply","Undefined variable"},
   {"<r/>",xsltSmokeStyle(text,`<xsl:include href="#missing"/>`,"T"),"compile","line 1: Document is empty\n\n^"},
   {"<r/>",xsltSmokeStyle(text,"",`<xsl:value-of select="document('file:///stuntgres-xslt-no-such-file.xml')"/>`),"apply",""},
   {`<?xml version="1.0" encoding="ISO-2022-KR"?><r/>éABCD`,"<bad/>","compile",xsltConversionDetail("0xC3 0xA9 0x41 0x42")},
  }{xml2Failure(t,p,b,c,xsltTwoQuery,xsltDiagnostic(v.stage,v.detail),false,v.source,v.style)}
  xsltDatum(t,p,b,c,"UTF-16","あé","fffe4230e900","fffe4230e9")
  xsltDatum(t,p,b,c,"UTF-16BE","😀","d83dde00","d83dde")
  if !b.PG { xsltSmokeControl(t,p,b,c,bracket) }
 })
}

func xsltSmokeControl(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,style string) {
 t.Helper()
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TABLE xslt_smoke_source(doc text,style text)",0))
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO xslt_smoke_source VALUES($1::text,$2::text)",1,`<doc><n>A</n><n>B</n></doc>`,style))
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE VIEW xslt_smoke_view AS SELECT xslt_process(doc,style) AS two,xslt_process(doc,style,'') AS three FROM xslt_smoke_source",0))
 q:="SELECT two,three FROM xslt_smoke_view"
 stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer stmt.Close()
 h:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
 fixture.RunStep(t,p.Ctx,h,b.Recorder,fixture.Select(q,[]string{"two","three"},[]string{"TEXT","TEXT"},[][]any{{"[A][B]","[A][B]"}}))
 old,err:=c.QueryContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer old.Close()
 dump:=fixture.ExtensionDump(t,p.Ctx,p.Native)
 if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil {t.Fatal(err)}
 if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(dump));err!=nil {t.Fatal(err)}
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("UPDATE xslt_smoke_source SET doc='<doc><n>B</n><n>A</n></doc>'",1))
 fixture.RunStep(t,p.Ctx,h,b.Recorder,fixture.Select(q,[]string{"two","three"},[]string{"TEXT","TEXT"},[][]any{{"[B][A]","[B][A]"}}))
 cols,err:=old.Columns();if err!=nil||len(cols)!=2||cols[0]!="two"||cols[1]!="three" {t.Fatal("old smoke columns",cols,err)}
 meta,err:=old.ColumnTypes();if err!=nil||len(meta)!=2||meta[0].DatabaseTypeName()!="TEXT"||meta[1].DatabaseTypeName()!="TEXT" {t.Fatal("old smoke metadata",meta,err)}
 if !old.Next() {t.Fatal("old smoke Rows missing",old.Err())}
 var two,three string
 if err:=old.Scan(&two,&three);err!=nil||two!="[A][B]"||three!="[A][B]" {t.Fatal("old smoke Rows mutated",two,three,err)}
 if old.Next()||old.Err()!=nil {t.Fatal("old smoke Rows tail",old.Err())}
}
