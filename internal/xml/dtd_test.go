package xml

import (
 "context"
 "encoding/json"
 "errors"
 "os"
 "reflect"
 "strings"
 "testing"
)

// Captured before production implementation from PostgreSQL 18.6/180006,
// UTF8/C, Debian libxml2 2.9.14+dfsg-1.3~deb12u6. Only pg_input_error_info
// and warning captures are used; the unsuccessful xmlsend observation query
// and the differently configured XPath parser are not parser expectations.
func TestXMLDTDDiagnostics(t *testing.T) {
 TestXMLDTDDiagnosticFrames(t)
 TestXMLDTDDiagnosticsURIOrder(t)
 dtdAttributeTypeDiagnostics(t)
 TestXMLDTDEntityStringMalformedUTF8(t)
 data,e:=os.ReadFile("data/dtd-oracle.json");if e!=nil{t.Fatal(e)}
 var cases []struct{Label,Mode,Input string;Message,Detail *string;Warnings string}
 if e=json.Unmarshal(data,&cases);e!=nil{t.Fatal(e)}
 for _,tc:=range cases {t.Run(tc.Label,func(t *testing.T){
  mode:=Document;if tc.Mode=="CONTENT"{mode=Content}
  for _,collect:=range []bool{false,true} {
   r,tree,e:=parse(context.Background(),tc.Input,Options{Mode:mode},collect)
   if tc.Message==nil {
    if e!=nil||!r.valid||r.Original()!=tc.Input||collect&&tree==nil{t.Fatalf("collect %v: %+v %v",collect,r,e)}
   }else{
    var pe *ParseError
    if !errors.As(e,&pe)||r.valid||tree!=nil{t.Fatalf("collect %v: %+v %v",collect,r,e)}
    if pe.Error()!=*tc.Message||pe.Detail()!=*tc.Detail{t.Fatalf("collect %v: %s\nwant:\n%s",collect,pe.Detail(),*tc.Detail)}
   }
   var events []Diagnostic
   if e==nil{events=r.Diagnostics()}else{var pe *ParseError;if errors.As(e,&pe){events=pe.Diagnostics}}
   var warnings []string
   for _,d:=range events{if d.Severity==Warning{warnings=append(warnings,"WARNING:  "+d.String())}}
   if strings.Join(warnings,"\n")!=strings.TrimSuffix(tc.Warnings,"\n"){t.Fatalf("warnings %+v want %q",warnings,tc.Warnings)}
  }
 })}
}
func dtdTree(t *testing.T,s string)*document {
 t.Helper();r,d,e:=parseExpanded(context.Background(),s,Options{Mode:Document})
 if e!=nil||!r.valid||r.Original()!=s||d==nil{t.Fatalf("%s: %+v %v",short(s),r,e)}
 return d
}
func dtdRoot(t *testing.T,d *document)*node {
 t.Helper();for _,n:=range d.nodes{if n.kind==elementNode{return n}};t.Fatal("missing root");return nil
}
func TestXMLDTDExpanded(t *testing.T) {
 TestXMLDTDCharacterReferences(t)
 TestXMLDTDProcessingInstructions(t)
 TestXMLDTDEntityStringUTF8Chunks(t)
 contentValue,contentTree,contentErr:=parseExpanded(context.Background(),`<a xml:space="&amp;"/>`,Options{Mode:Content})
 if contentErr!=nil||len(contentValue.Diagnostics())!=1||contentValue.Diagnostics()[0].Message!=`Invalid value "&#38;" for xml:space : "default" or "preserve" expected`||dtdRoot(t,contentTree).attributes[0].value!="&"{t.Fatal(contentValue,contentErr)}
 d:=dtdTree(t,`<!DOCTYPE a [<!ENTITY inner "<b>ok</b>"><!ENTITY outer "&inner;&inner;">]><a>&outer;</a>`)
 a:=dtdRoot(t,d)
 if len(a.children)!=2||a.children[0].name!="b"||a.children[1].name!="b"||a.children[0]==a.children[1]{t.Fatalf("expanded children %+v",a.children)}
 for _,b:=range a.children{if len(b.children)!=1||b.children[0].value!="ok"{t.Fatal(b)}}
 d=dtdTree(t,`<!--p--><?go x?><!DOCTYPE a [<!ENTITY word "ok">]><a><![CDATA[x]]>&word;</a>`)
 if len(d.nodes)!=4||d.nodes[0].kind!=commentNode||d.nodes[1].kind!=piNode||d.nodes[2].kind!=doctypeNode{t.Fatal(d.nodes)}
 a=dtdRoot(t,d);if len(a.children)!=2||a.children[0].kind!=cdataNode||a.children[0].value!="x"||a.children[1].value!="ok"{t.Fatal(a)}
 if d.nodes[2].doctype.name!="a"||d.nodes[2].doctype.subset!=`<!ENTITY word "ok">`||len(d.nodes[2].doctype.declarations)!=1{t.Fatal(d.nodes[2].doctype)}
 a=dtdRoot(t,dtdTree(t,`<!DOCTYPE a [<!ENTITY e '&quot; &apos; &lt; &amp;'>]><a>&e;</a>`))
 if len(a.children)!=1||a.children[0].kind!=textNode||a.children[0].value!=`" ' < &`{t.Fatal(a)}
 a=dtdRoot(t,dtdTree(t,`<!DOCTYPE a [<!ENTITY e "one">]><a>x&e;y&e;z<![CDATA[]]>tail</a>`))
 if len(a.children)!=3||a.children[0].value!="xoneyonez"||a.children[1].kind!=cdataNode||a.children[1].value!=""||a.children[2].value!="tail"{t.Fatal(a)}
 d=dtdTree(t,`<!DOCTYPE a []><a/>`)
 if !d.nodes[0].doctype.hasSubset||d.nodes[0].doctype.raw!=`<!DOCTYPE a []>`{t.Fatal(d.nodes[0].doctype)}
}
func TestXMLDTDAttributes(t *testing.T) {
 for _,tc:=range []struct{input string;attrs []string}{
  {`<!DOCTYPE a [<!ATTLIST a c CDATA "  a  b  " n NMTOKENS "  a  b  " f CDATA #FIXED "x" req CDATA #REQUIRED>]><a/>`,[]string{"c=  a  b  ","n=a b","f=x"}},
  {"<!DOCTYPE a [<!ATTLIST a c CDATA #IMPLIED n NMTOKENS #IMPLIED>]><a c=\"  a\t b&#9;c  \" n=\"  a\t b&#9;c  \"/>",[]string{"c=  a  b\tc  ","n=a b\tc"}},
  {`<!DOCTYPE a [<!ENTITY e " a&#9;b "><!ATTLIST a c CDATA "&e;" n NMTOKENS "&e;">]><a/>`,[]string{"c= a b ","n=a b"}},
  {`<!DOCTYPE a [<!ATTLIST a x CDATA #IMPLIED><!ATTLIST a x CDATA "later">]><a/>`,nil},
  {`<!DOCTYPE a [<!ATTLIST a f CDATA #FIXED "x">]><a f="y"/>`,[]string{"f=y"}},
  {`<!DOCTYPE a [<!ENTITY e '&quot; &apos; &lt; &amp;'>]><a x="&e;">&e;</a>`,[]string{`x=" ' < &`}},
  // tree.c xmlSplitQName3 leaves a leading colon unsplit. Default-table
  // QName identity differs from a recovered, unprefixed invalid NCName.
  {`<!DOCTYPE :a [<!ATTLIST :a :x NMTOKENS "  v  ">]><:a/>`,[]string{":x=v"}},
  {`<!DOCTYPE p:1 [<!ATTLIST p:1 x CDATA "default">]><p:1/>`,nil},
 } {a:=dtdRoot(t,dtdTree(t,tc.input));var got []string;for _,v:=range a.attributes{got=append(got,v.name+"="+v.value)};if !reflect.DeepEqual(got,tc.attrs){t.Fatalf("%s: %#v want %#v",tc.input,got,tc.attrs)}}
 a:=dtdRoot(t,dtdTree(t,`<!DOCTYPE a [<!ATTLIST a p:x CDATA "x" xmlns:p CDATA "urn:p" y CDATA "y">]><a z="z"/>`))
 if len(a.namespaces)!=1||a.namespaces[0].name!="xmlns:p"||a.namespaces[0].value!="urn:p"||len(a.attributes)!=3||a.attributes[0].name!="z"||a.attributes[1].name!="p:x"||a.attributes[1].uri!="urn:p"||a.attributes[2].name!="y"{t.Fatalf("namespaces/default order %+v",a)}
}
func TestXMLDTDGrammar(t *testing.T) {
 for _,s:=range []string{
  `<!DOCTYPEa><a/>`,
  `<!DOCTYPE a [<!ELEMENT a ((b|c)+,d?,(e,f)*)><!ELEMENT b EMPTY><!ELEMENT c ANY>]><a/>`,
  `<!DOCTYPE a [<!ELEMENT a (#PCDATA|b|c)*>]><a>text<b/></a>`,
  `<!DOCTYPE a [<!ELEMENT a (#PCDATA)*>]><a/>`,
  `<!DOCTYPE a [<!ATTLIST a c CDATA #IMPLIED id ID #IMPLIED ir IDREF #IMPLIED irs IDREFS #IMPLIED e ENTITY #IMPLIED es ENTITIES #IMPLIED n NMTOKEN #IMPLIED ns NMTOKENS #IMPLIED en (one|two) "not-enum" no NOTATION (n|m) #IMPLIED>]><a/>`,
  `<!DOCTYPE a [<!ATTLIST a><!NOTATION n PUBLIC "id"><!NOTATION m SYSTEM "x">]><a/>`,
 }{dtdTree(t,s)}
}
func TestXMLDTDNonvalidating(t *testing.T){for _,s:=range []string{
 `<!DOCTYPE wrong [<!ELEMENT a EMPTY><!ELEMENT a ANY>]><a>text<b/></a>`,
 `<!DOCTYPE a [<!ELEMENT a (#PCDATA|b|b)*><!ATTLIST a x (v|v) "other" r CDATA #REQUIRED id ID "duplicate">]><a><a/></a>`,
 `<!DOCTYPE a [<!NOTATION n SYSTEM "one"><!NOTATION n SYSTEM "two"><!ENTITY e SYSTEM "x" NDATA absent>]><a/>`,
 }{dtdTree(t,s)}}
func TestXMLDTDDeclarations(t *testing.T){
 TestXMLDTDEntitySeparators(t)
 TestXMLDTDEntityTermination(t)
 a:=dtdRoot(t,dtdTree(t,`<!DOCTYPE a [<!ENTITY e "first"><!ENTITY e "second"><!ATTLIST a x CDATA "first"><!ATTLIST a x CDATA "second">]><a>&e;</a>`))
 if a.children[0].value!="first"||a.attributes[0].value!="first"{t.Fatal(a)}
 for _,standalone:=range []string{"",` standalone="no"`,` standalone="yes"`}{a=dtdRoot(t,dtdTree(t,`<?xml version="1.0"`+standalone+`?><!DOCTYPE a [<!ATTLIST a x NMTOKENS "  a b  ">]><a/>`));if a.attributes[0].value!="a b"{t.Fatal(a)}}
}
func TestXMLDTDEntities(t *testing.T){for _,tc:=range []struct{in,text string}{
 {`<!DOCTYPE a [<!ENTITY word "ok">]><a>&word;</a>`,"ok"},
 {`<!DOCTYPE a [<!ENTITY e "&f;"><!ENTITY f "ok">]><a>&e;</a>`,"ok"},
 {`<!DOCTYPE a [<!ENTITY amp "not-an-amp">]><a>&amp;</a>`,"&"},
 {`<!DOCTYPE a [<!ENTITY e "&amp;">]><a>&e;</a>`,"&"},
 }{a:=dtdRoot(t,dtdTree(t,tc.in));if len(a.children)!=1||a.children[0].value!=tc.text{t.Fatal(a)}}
 for _,v:=range []string{"&missing;","&e;","<b>"}{dtdTree(t,`<!DOCTYPE a [<!ENTITY e "`+v+`">]><a/>`)}
}
func TestXMLDTDParameterEntities(t *testing.T){
 TestXMLDTDSourceBoundaries(t)
 TestXMLDTDParameterCycle(t)
 TestXMLDTDStringPE(t)
 TestXMLDTDParameterReferenceLimit(t)
 for _,s:=range []string{
 `<!DOCTYPE a [<!ENTITY % p '<!ENTITY e "ok">'>%p;]><a>&e;</a>`,
 `<!DOCTYPE a [<!ENTITY % p '<!ENTITY &#37; q "<!ELEMENT a EMPTY>">&#37;q;'>%p;]><a/>`,
 `<!DOCTYPE a [<!ENTITY % p '<!ENTITY &#37; q "&#37;q;">&#37;q;'>%p;]><a/>`,
 }{dtdTree(t,s)}}
func TestXMLDTDNotations(t *testing.T){
 TestXMLDTDNDATASeparators(t)
 TestXMLDTDNDATAMissingName(t)
 d:=dtdTree(t,`<!DOCTYPE a [<!NOTATION n PUBLIC "id"><!ENTITY e SYSTEM "x" NDATA n>]><a/>`)
 declarations:=d.nodes[0].doctype.declarations
 if len(declarations)!=2||declarations[0].kind!="NOTATION"||declarations[0].unparsed||declarations[0].name!="n"||declarations[0].publicID!="id"||declarations[1].kind!="ENTITY"||!declarations[1].unparsed||declarations[1].notation!="n"||!declarations[1].external||declarations[1].systemID!="x"{t.Fatal(declarations)}
}
func TestXMLDTDModes(t *testing.T){
 for _,prefix:=range []string{""," \n<!--ok--><?go x?>",`<?xml version="1.0"?>`}{s:=prefix+`<!DOCTYPE a><a/>`;r,d,e:=parseExpanded(context.Background(),s,Options{Mode:Content});if e!=nil||d==nil||r.RequestedMode()!=Content||r.EffectiveMode()!=Document||r.Original()!=s{t.Fatal(r,e)}}
 for _,s:=range []string{`text<!DOCTYPE a><a/>`,`<a/><!DOCTYPE a>`,`<a><!DOCTYPE a></a>`}{r,_,e:=parseExpanded(context.Background(),s,Options{});if e==nil||r.valid{t.Fatal(s)}}
}
func TestXMLDTDFeatureSmoke(t *testing.T){
 TestXMLDTDExpanded(t);TestXMLDTDAttributes(t);TestXMLDTDParameterEntities(t);TestXMLDTDModes(t)
 TestXMLDTDEntityStringMalformedUTF8(t)
 TestXMLDTDNDATASeparators(t)
 TestXMLDTDNDATAMissingName(t)
 TestXMLDTDEntitySeparators(t)
 TestXMLDTDEntityTermination(t)
 word:=`<!DOCTYPE a [<!ENTITY word "ok">]><a>&word;</a>`
 parsed,err:=Parse(context.Background(),word,Options{Mode:Content})
 if err!=nil||!parsed.valid||parsed.Original()!=word||parsed.RequestedMode()!=Content||parsed.EffectiveMode()!=Document{t.Fatal(parsed,err)}
 a:=dtdRoot(t,dtdTree(t,`<!DOCTYPE a [<!ENTITY % p '<!ATTLIST a x NMTOKENS "  a b  ">'>%p;]><a/>`))
 if len(a.attributes)!=1||a.attributes[0].name!="x"||a.attributes[0].value!="a b"{t.Fatal(a)}
 a=dtdRoot(t,dtdTree(t,`<!DOCTYPE a SYSTEM "relative.dtd" [<!ENTITY e SYSTEM "relative.ent">]><a>x&e;y&missing;</a>`))
 if len(a.children)!=2||a.children[0].value!="xy"||a.children[1].kind!=entityReferenceNode||a.children[1].name!="missing"{t.Fatal(a)}
 bad:=`<!DOCTYPE a [<!ELEMENT unused (b|)>]><a/>`
 var want []string
 for _,message:=range []string{"ContentDecl : Name or '(' expected","expected '>'","internal error: xmlParseInternalSubset: error detected in Markup declaration","DOCTYPE improperly terminated","Start tag expected, '<' not found"}{want=append(want,"line 1: "+message+"\n"+bad+"\n"+strings.Repeat(" ",33)+"^")}
 for _,collect:=range []bool{false,true}{r,d,e:=parse(context.Background(),bad,Options{Mode:Document},collect);var pe *ParseError;if !errors.As(e,&pe)||r.valid||d!=nil||pe.Detail()!=strings.Join(want,"\n"){t.Fatal(r,e)}}
 r,d,e:=parseExpanded(context.Background(),`<!DOCTYPE a><a/><b/>`,Options{});var pe *ParseError
 if !errors.As(e,&pe)||r.valid||d!=nil||pe.Error()!="invalid XML content"||pe.Detail()!="line 1: Extra content at the end of the document\n<!DOCTYPE a><a/><b/>\n                ^"{t.Fatal(r,e)}
 accept(t,"<healthy/>",Document)
}

func TestXMLDTDEntitySeparators(t *testing.T) {
 // PostgreSQL18.6/180006 UTF8/C with pinned Debian libxml2 independently
 // captured these complete blocks before the remedy. Doubled %% is literal
 // output from xmlFatalErrMsg, not formatting notation. Coordinates/source
 // fields are matching-source inferences; all captured warning stderr is empty.
 type separatorError struct{message string;offset int}
 for _,tc:=range []struct{declaration,name string;parameter bool;events []separatorError}{
  {`<!ENTITYe "x">`,"e",false,[]separatorError{{"Space required after '<!ENTITY'",21}}},
  {`<!ENTITY %p "x">`,"p",true,[]separatorError{{"Space required after '%%'",23}}},
  {`<!ENTITY e"x">`,"e",false,[]separatorError{{"Space required after the entity name",23}}},
  {`<!ENTITY % p"x">`,"p",true,[]separatorError{{"Space required after the entity name",25}}},
  {`<!ENTITY%p"x">`,"p",true,[]separatorError{{"Space required after '<!ENTITY'",21},{"Space required after '%%'",22},{"Space required after the entity name",23}}},
  {`<!ENTITY e "x">`,"e",false,nil},
  {`<!ENTITY % p "x">`,"p",true,nil},
 }{for _,mode:=range []Mode{Document,Content}{for _,collect:=range []bool{false,true}{
  input:=`<!DOCTYPE a [`+tc.declaration+`]><a/>`
  r,d,err:=parse(context.Background(),input,Options{Mode:mode},collect)
  if len(tc.events)==0{
   if err!=nil||!r.valid||r.Original()!=input||r.RequestedMode()!=mode||r.EffectiveMode()!=Document||len(r.Diagnostics())!=0{t.Fatalf("%s mode %v collect %t: %+v %v",tc.declaration,mode,collect,r,err)}
   if collect{
    root:=dtdRoot(t,d);if len(root.children)!=0||len(root.attributes)!=0{t.Fatalf("unused declaration affected root: %+v",root)}
    declarations:=d.nodes[0].doctype.declarations
    if len(declarations)!=1||declarations[0].kind!="ENTITY"||declarations[0].name!=tc.name||declarations[0].parameter!=tc.parameter||declarations[0].external||declarations[0].unparsed||declarations[0].value!="x"{t.Fatalf("declarations %+v",declarations)}
   }else if d!=nil{t.Fatal("validation retained a tree")}
  }else{
   var pe *ParseError
   if !errors.As(err,&pe)||!reflect.DeepEqual(r,Result{})||d!=nil{t.Fatalf("%s mode %v collect %t: %+v tree %+v err %v",tc.declaration,mode,collect,r,d,err)}
   var diagnostics []Diagnostic;var blocks []string
   for _,event:=range tc.events{
    caret:=strings.Repeat(" ",event.offset)+"^"
    diagnostics=append(diagnostics,Diagnostic{Condition:"XML_ERR_SPACE_REQUIRED",Domain:ParserDomain,Severity:Fatal,Line:1,Column:event.offset+1,Offset:event.offset,SourceBaseOffset:0,Message:event.message,SourceContext:input,Caret:caret,Source:OriginalSource})
    blocks=append(blocks,"line 1: "+event.message+"\n"+input+"\n"+caret)
   }
   primary:="invalid XML document";if mode==Content{primary="invalid XML content"}
   if pe.Category!=GrammarError||pe.RequestedMode!=mode||pe.EffectiveMode!=Document||pe.Error()!=primary||pe.Detail()!=strings.Join(blocks,"\n")||!reflect.DeepEqual(pe.Diagnostics,diagnostics){t.Fatalf("%s mode %v collect %t: %+v Detail %q diagnostics %+v",tc.declaration,mode,collect,pe,pe.Detail(),pe.Diagnostics)}
  }
  accept(t,"<healthy/>",mode)
 }}}
 // Source-only control: separator failures continue, but an absent Name
 // still returns immediately from this specific production.
 for _,tc:=range []struct{input string;offset int;parameter bool}{{` "x">`,1,false},{` % "x">`,3,true}}{
  p:=parser{ctx:context.Background(),s:tc.input,line:1,column:1,effective:Document}
  name,value,publicID,systemID,notation,parameter,external,unparsed:=p.entityDeclaration()
  want:=Diagnostic{Condition:"XML_ERR_NAME_REQUIRED",Domain:ParserDomain,Severity:Fatal,Line:1,Column:tc.offset+1,Offset:tc.offset,Message:"xmlParseEntityDecl: no name",SourceContext:tc.input,Caret:strings.Repeat(" ",tc.offset)+"^",Source:OriginalSource}
  if p.err!=nil||!p.failed()||p.i!=tc.offset||name!=""||value!=""||publicID!=""||systemID!=""||notation!=""||parameter!=tc.parameter||external||unparsed||!reflect.DeepEqual(p.events,[]Diagnostic{want}){t.Fatalf("absent Name %q: cursor %d err %v events %+v",tc.input,p.i,p.err,p.events)}
 }
}

func TestXMLDTDEntityTermination(t *testing.T) {
 // Independent PostgreSQL18.6/180006 UTF8/C, Debian libxml2 captures precede
 // this remedy. Final ENTITY failure halts; a prior separator diagnostic is
 // retained, but no subset/DOCTYPE/root recovery is appended. Source fields
 // are matching-source inferences; every capture had empty warning stderr.
 for _,tc:=range []struct{declaration,name string;offset int;legal,initial bool}{
  {`<!ENTITY e "x" junk>`,"e",28,false,false},
  {`<!ENTITY e "x">`,"e",0,true,false},
  {`<!ENTITY % p "x" junk>`,"p",30,false,false},
  {`<!ENTITY % p "x">`,"p",0,true,false},
  {`<!ENTITY e SYSTEM "x" junk>`,"e",35,false,false},
  {`<!ENTITY e SYSTEM "x">`,"e",0,true,false},
  {`<!ENTITY e PUBLIC "id" "x" junk>`,"e",40,false,false},
  {`<!ENTITY e PUBLIC "id" "x">`,"e",0,true,false},
  {`<!ENTITYe "x" junk>`,"e",27,false,true},
 }{for _,mode:=range []Mode{Document,Content}{for _,collect:=range []bool{false,true}{
  input:=`<!DOCTYPE a [`+tc.declaration+`]><a/>`
  r,d,err:=parse(context.Background(),input,Options{Mode:mode},collect)
  if tc.legal{
   if err!=nil||!r.valid||r.Original()!=input||r.RequestedMode()!=mode||r.EffectiveMode()!=Document||len(r.Diagnostics())!=0{t.Fatalf("%s mode %v collect %t: %+v %v",tc.declaration,mode,collect,r,err)}
   if collect{root:=dtdRoot(t,d);if len(root.children)!=0||len(root.attributes)!=0{t.Fatalf("unused entity affected root: %+v",root)}}else if d!=nil{t.Fatal("validation retained a tree")}
  }else{
   var pe *ParseError
   if !errors.As(err,&pe)||!reflect.DeepEqual(r,Result{})||d!=nil{t.Fatalf("%s mode %v collect %t: %+v tree %+v err %v",tc.declaration,mode,collect,r,d,err)}
   var diagnostics []Diagnostic;var blocks []string
   if tc.initial{
    message:="Space required after '<!ENTITY'";caret:=strings.Repeat(" ",21)+"^"
    diagnostics=append(diagnostics,Diagnostic{Condition:"XML_ERR_SPACE_REQUIRED",Domain:ParserDomain,Severity:Fatal,Line:1,Column:22,Offset:21,Message:message,SourceContext:input,Caret:caret,Source:OriginalSource})
    blocks=append(blocks,"line 1: "+message+"\n"+input+"\n"+caret)
   }
   message:="xmlParseEntityDecl: entity "+tc.name+" not terminated";caret:=strings.Repeat(" ",tc.offset)+"^"
   diagnostics=append(diagnostics,Diagnostic{Condition:"XML_ERR_ENTITY_NOT_FINISHED",Domain:ParserDomain,Severity:Fatal,Line:1,Column:tc.offset+1,Offset:tc.offset,Message:message,SourceContext:input,Caret:caret,Source:OriginalSource})
   blocks=append(blocks,"line 1: "+message+"\n"+input+"\n"+caret)
   primary:="invalid XML document";if mode==Content{primary="invalid XML content"}
   if pe.Category!=GrammarError||pe.RequestedMode!=mode||pe.EffectiveMode!=Document||pe.Error()!=primary||pe.Detail()!=strings.Join(blocks,"\n")||!reflect.DeepEqual(pe.Diagnostics,diagnostics){t.Fatalf("%s mode %v collect %t: %+v Detail %q diagnostics %+v",tc.declaration,mode,collect,pe,pe.Detail(),pe.Diagnostics)}
  }
  accept(t,"<healthy/>",mode)
 }}}
}
