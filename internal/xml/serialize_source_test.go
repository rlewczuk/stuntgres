package xml

import (
 "context"
 "strings"
 "testing"
)

// These bounded extensions are source-derived from libxml2 2.9.14 xmlsave.c,
// valid.c and HTMLtree.c, not additional PostgreSQL executions.
func TestXMLSerializeXHTMLSourceRules(t *testing.T){
 const declaration=`<!DOCTYPE html SYSTEM "http://www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd">`
 const saved="<!DOCTYPE html SYSTEM \"http://www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd\">\n"
 for _,tc:=range []struct{body,want string}{
  {`<html xmlns="http://www.w3.org/1999/xhtml"><body><area/><br/><base/><basefont/><col/><frame/><hr/><img/><input/><isindex/><link/><meta/><param/><script/><textarea/></body></html>`,"<html xmlns=\"http://www.w3.org/1999/xhtml\">\n  <body>\n    <area />\n    <br />\n    <base />\n    <basefont />\n    <col />\n    <frame />\n    <hr />\n    <img />\n    <input />\n    <isindex />\n    <link />\n    <meta />\n    <param />\n    <script></script>\n    <textarea></textarea>\n  </body>\n</html>"},
  {`<html><body><input checked="" compact="" declare="" defer="" disabled="" ismap="" multiple="" nohref="" noresize="" noshade="" nowrap="" readonly="" selected=""/></body></html>`,"<html xmlns=\"http://www.w3.org/1999/xhtml\">\n  <body>\n    <input checked=\"checked\" compact=\"compact\" declare=\"declare\" defer=\"defer\" disabled=\"disabled\" ismap=\"ismap\" multiple=\"multiple\" nohref=\"nohref\" noresize=\"noresize\" noshade=\"noshade\" nowrap=\"nowrap\" readonly=\"readonly\" selected=\"selected\" />\n  </body>\n</html>"},
  {`<html><body><a name="a"/><p name="p"/><div name="d"/><img name="i"/><map name="m"/><applet name="a"/><form name="f"/><frame name="f"/><iframe name="i"/></body></html>`,"<html xmlns=\"http://www.w3.org/1999/xhtml\">\n  <body>\n    <a name=\"a\" id=\"a\"></a>\n    <p name=\"p\" id=\"p\"></p>\n    <div name=\"d\" id=\"d\"></div>\n    <img name=\"i\" id=\"i\" />\n    <map name=\"m\" id=\"m\"></map>\n    <applet name=\"a\" id=\"a\"></applet>\n    <form name=\"f\" id=\"f\"></form>\n    <frame name=\"f\" id=\"f\" />\n    <iframe name=\"i\" id=\"i\"></iframe>\n  </body>\n</html>"},
  {`<html><head><meta charset="UTF-8"/></head><body/></html>`,"<html xmlns=\"http://www.w3.org/1999/xhtml\">\n  <head>\n    <meta http-equiv=\"Content-Type\" content=\"text/html; charset=UTF-8\" />\n    <meta charset=\"UTF-8\" />\n  </head>\n  <body></body>\n</html>"},
 }{value:=serializeValue(t,declaration+tc.body,Content);requireSerialized(t,value,SerializeOptions{Mode:Document,Indent:true},saved+tc.want)}
}
func TestXMLSerializeDTDSourceRules(t *testing.T){
 const input=`<!DOCTYPE a [<!ELEMENT a ANY><!--c--><?p?><?p ?><!ATTLIST a c CDATA #IMPLIED i ID #REQUIRED r IDREF #IMPLIED rs IDREFS #IMPLIED e ENTITY #IMPLIED es ENTITIES #IMPLIED n NMTOKEN #IMPLIED ns NMTOKENS #IMPLIED><!ENTITY % p SYSTEM "urn:p"><!ENTITY e PUBLIC "p" "s"><!NOTATION n PUBLIC "p">]><a/>`
 const want="<!DOCTYPE a [\n<!NOTATION n PUBLIC \"p\" >\n<!ELEMENT a ANY>\n<!--c--><?p?><?p ?><!ATTLIST a c CDATA #IMPLIED>\n<!ATTLIST a i ID #REQUIRED>\n<!ATTLIST a r IDREF #IMPLIED>\n<!ATTLIST a rs IDREFS #IMPLIED>\n<!ATTLIST a e ENTITY #IMPLIED>\n<!ATTLIST a es ENTITIES #IMPLIED>\n<!ATTLIST a n NMTOKEN #IMPLIED>\n<!ATTLIST a ns NMTOKENS #IMPLIED>\n<!ENTITY % p SYSTEM \"urn:p\">\n<!ENTITY e PUBLIC \"p\" \"s\">\n]>\n<a/>"
 requireSerialized(t,serializeValue(t,input,Content),SerializeOptions{Mode:Document,Indent:true},want)
 // Sibling-width does not consume recursive model depth in the transient saver.
 wide:="<!DOCTYPE a [<!ELEMENT a ("+strings.Repeat("b,",2000)+"b)>]><a/>"
 value:=serializeValue(t,wide,Content)
 expected:="<!DOCTYPE a [\n<!ELEMENT a ("+strings.Repeat("b , ",2000)+"b)>\n]>\n<a/>"
 requireSerialized(t,value,SerializeOptions{Mode:Document,Indent:true},expected)
 // Suppression/cancellation cannot publish a validated prefix or mutate a value.
 c:=&phaseContext{Context:context.Background(),phase:"model",after:10,cause:context.Canceled}
 out,err:=Serialize(c,value,SerializeOptions{Indent:true});if out!=""||err!=context.Canceled||!c.admitted{t.Fatal(out,err)}
}

func TestXMLSerializeDTDEntityValueIdentity(t *testing.T){
 // Full outputs in both modes are independently pinned PostgreSQL18.6 UTF8/C
 // observations; processed replacement metadata follows the existing collector
 // contract. Provenance/reproduction is in data/README.md.
 // The q declaration is parsed in r's replacement input, not the raw subset.
 const input=`<!DOCTYPE a [<!ENTITY % p "ok"><!ENTITY % r '<!ENTITY &#37; q "&#38;#65;&#37;p;">'>%r;]><a/>`
 value,tree,err:=parseExpanded(context.Background(),input,Options{})
 if err!=nil{t.Fatal(err)}
 declarations:=tree.nodes[0].doctype.declarations
 if len(declarations)!=3||declarations[2].name!="q"||declarations[2].value!="Aok"||declarations[2].originalValue!="&#65;%p;"{t.Fatalf("replacement/original identities %+v",declarations)}
 const expected="<!DOCTYPE a [\n<!ENTITY % p \"ok\">\n<!ENTITY % r '<!ENTITY &#37; q \"&#38;#65;&#37;p;\">'>\n<!ENTITY % q \"&#65;%p;\">\n]>\n<a/>"
 requireSerialized(t,value,SerializeOptions{Mode:Document,Indent:true},expected)
 requireSerialized(t,value,SerializeOptions{Mode:Content,Indent:true},expected+"\n")
}
