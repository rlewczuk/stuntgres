package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceXMLProduceTextComment(t *testing.T){xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 for _,tc:=range []struct{q string;want any}{
  {`SELECT xmltext('< foo & bar >') AS v`,`&lt; foo &amp; bar &gt;`},
  {`SELECT pg_catalog.xmltext(E'<>&"''\t\n\r é😀') AS v`,"&lt;&gt;&amp;&quot;'\t\n&#13; é😀"},
  {`SELECT xmltext('') AS v`,""},{`SELECT xmltext(NULL) AS v`,nil},
  {`SELECT xmlcomment('hello') AS v`,`<!--hello-->`},{`SELECT xmlcomment('') AS v`,`<!---->`},
  {`SELECT xmlcomment('-a') AS v`,`<!---a-->`},{`SELECT xmlcomment(NULL) AS v`,nil},
  {`SELECT xmltext(chr(1)||chr(8)||chr(11)||chr(12)||chr(31)||chr(127)) AS v`,"\x01\x08\x0b\x0c\x1f\x7f"},
  {`SELECT CASE WHEN false THEN xmlcomment('a--b') ELSE xmlcomment('good') END AS v`,`<!--good-->`},
 }{xmlBoth(t,p,b,c,xmlSelect(tc.q,"XML",tc.want))}
 for _,q:=range []string{`SELECT xmlcomment('a--b')`,`SELECT xmlcomment('a-')`,`SELECT xmlcomment('a--b') WHERE false`}{xmlFailure(t,p,b,c,q,xmlError("2200S","invalid XML comment","",0),false)}
 xmlFailure(t,p,b,c,`SELECT xmlcomment($1::text) WHERE false`,xmlError("2200S","invalid XML comment","",0),false,"a--b")
 xmlBoth(t,p,b,c,fixture.Select(`SELECT oid::integer,proisstrict,provolatile::text FROM pg_proc WHERE oid IN (2895,3813) ORDER BY oid`,[]string{"oid","proisstrict","provolatile"},[]string{"INT4","BOOL","TEXT"},[][]any{{int64(2895),true,"i"},{int64(3813),true,"i"}}))
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(`CREATE FUNCTION public.xmltext(text) RETURNS text LANGUAGE sql IMMUTABLE AS 'SELECT ''shadow:'' || $1'`,0))
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(`SET search_path=public,pg_catalog`,0))
 xmlBoth(t,p,b,c,xmlSelect(`SELECT xmltext('<&') AS v`,"TEXT",`shadow:<&`))
 xmlBoth(t,p,b,c,xmlSelect(`SELECT pg_catalog.xmltext('<&') AS v`,"XML",`&lt;&amp;`))
})}

func TestReferenceXMLProduceConcat(t *testing.T){xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 for _,tc:=range []struct{q string;want any}{
  {`SELECT xmlconcat('<abc/>','<bar>foo</bar>') AS v`,`<abc/><bar>foo</bar>`},
  {`SELECT xmlconcat(NULL,'<a/>',NULL,'<b/>') AS v`,`<a/><b/>`},
  {`SELECT xmlconcat(NULL,NULL) AS v`,nil},{`SELECT xmlconcat(''::xml,NULL) AS v`,""},
  {`SELECT xmlconcat('<?xml version="1.0"?><a/>') AS v`,`<a/>`},
  {`SELECT xmlconcat('<?xml version="1.1"?><a/>') AS v`,`<?xml version="1.1"?><a/>`},
  {`SELECT xmlconcat('<?xml version="1.1" standalone="yes"?><a/>','<?xml version="1.1" standalone="no"?><b/>') AS v`,`<?xml version="1.1" standalone="no"?><a/><b/>`},
  {`SELECT xmlconcat('<?xml version="1.1" standalone="yes"?><a/>','<?xml version="1.0" standalone="yes"?><b/>') AS v`,`<?xml version="1.0" standalone="yes"?><a/><b/>`},
  {`SELECT xmlconcat('<?xml version="1.1" standalone="yes"?><a/>','<b/>') AS v`,`<a/><b/>`},
  {`SELECT xmlconcat(E'<?xml version="1.0"?>\n<a/>','<b/>')::text AS v`,"\n<a/><b/>"},
  {`SELECT xmlconcat(xmlroot('<a/>',VERSION 'x" y'),'<b/>')::text AS v`,`<b/>`},
  {`SELECT xmlconcat(xmlroot('<a/>',VERSION 'x" y'))::text AS v`,`<?xml version="x"?>`},
 }{typ:="XML";if len(tc.q)>0&&(tc.q[len(tc.q)-11:]=="::text AS v"){typ="TEXT"};xmlBoth(t,p,b,c,xmlSelect(tc.q,typ,tc.want))}
})}

func TestReferenceXMLProduceElement(t *testing.T){xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 for _,tc:=range []struct{q string;want any}{
  {`SELECT xmlelement(NAME foo,text 'b<a/>r') AS v`,`<foo>b&lt;a/&gt;r</foo>`},
  {`SELECT xmlelement(NAME foo,xml 'b<a/>r') AS v`,`<foo>b<a/>r</foo>`},
  {`SELECT xmlelement(NAME foo,XMLATTRIBUTES(E'<>&"''\t\n\r é😀' AS a),E'<>&"''\t\n\r é😀') AS v`,"<foo a=\"&lt;&gt;&amp;&quot;'&#9;&#10;&#13; &#xE9;&#x1F600;\">&lt;&gt;&amp;\"'\t\n&#x0d; é😀</foo>"},
  {`SELECT xmlelement(NAME foo) AS v`,`<foo/>`},{`SELECT xmlelement(NAME foo,NULL) AS v`,`<foo/>`},
  {`SELECT xmlelement(NAME foo,'') AS v`,`<foo></foo>`},{`SELECT xmlelement(NAME foo,''::xml) AS v`,`<foo></foo>`},
  {`SELECT xmlelement(NAME foo,XMLATTRIBUTES(NULL AS absent,'' AS empty),NULL) AS v`,`<foo empty=""/>`},
  {`SELECT xmlelement(NAME foo,xml '<?xml version="1.0"?><a/>') AS v`,`<foo><a/></foo>`},
  {`SELECT xmlelement(NAME foo,xml '<?xml version="1.1"?><a/>') AS v`,`<foo><?xml version="1.1"?><a/></foo>`},
  {`SELECT xmlelement(NAME foo,XMLATTRIBUTES(xml '<?xml version="1.0"?><a/>' AS a)) AS v`,`<foo a="&lt;a/&gt;"/>`},
  {`SELECT xmlelement(NAME foo,chr(1),XMLPARSE(CONTENT '')) AS v`,"<foo>\x01</foo>"},
 }{xmlBoth(t,p,b,c,xmlSelect(tc.q,"XML",tc.want))}
})}

func TestReferenceXMLProduceForestNames(t *testing.T){xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 for _,tc:=range []struct{q string;want any}{
  {`SELECT xmlforest('abc' AS foo,123 AS bar) AS v`,`<foo>abc</foo><bar>123</bar>`},
  {`SELECT xmlforest(NULL AS a,NULL AS b) AS v`,nil},{`SELECT xmlforest('' AS foo,NULL AS gone) AS v`,`<foo></foo>`},
  {`SELECT xmlforest(1 AS a,2 AS a) AS v`,`<a>1</a><a>2</a>`},
  {`SELECT xmlelement(NAME "foo$bar",XMLATTRIBUTES('xyz' AS "a&b")) AS v`,`<foo_x0024_bar a_x0026_b="xyz"/>`},
  {`SELECT xmlelement(NAME "😀‌",XMLATTRIBUTES(1 AS "_x")) AS v`,`<_x1F600__x200C_ _x005F_x="1"/>`},
  {`SELECT xmlelement(NAME ":a:b.c") AS v`,`<_x003A_a:b.c/>`},
  {`SELECT xmlelement(NAME "xmlThing") AS v`,`<xmlThing/>`},
  {`SELECT xmlforest("xmlThing","XMLThing","a:b",("é")) AS v FROM (SELECT 1 AS "xmlThing",2 AS "XMLThing",3 AS "a:b",4 AS "é") s`,`<_x0078_mlThing>1</_x0078_mlThing><_x0058_MLThing>2</_x0058_MLThing><a_x003A_b>3</a_x003A_b><é>4</é>`},
  {`SELECT xmlforest((s.a)) AS v FROM (SELECT 'x' AS a) s`,`<a>x</a>`},
  {`SELECT xmlelement(NAME SELECT,XMLATTRIBUTES(1 AS FROM)) AS v`,`<select from="1"/>`},
  {`SELECT xmlforest('x' AS "́a",'y' AS "á") AS v`,`<_x0301_a>x</_x0301_a><á>y</á>`},
 }{xmlBoth(t,p,b,c,xmlSelect(tc.q,"XML",tc.want))}
})}

func TestReferenceXMLProducePI(t *testing.T){xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 for _,tc:=range []struct{q string;want any}{
  {`SELECT xmlpi(NAME foo) AS v`,`<?foo?>`},{`SELECT xmlpi(NAME foo,'') AS v`,`<?foo ?>`},
  {`SELECT xmlpi(NAME foo,NULL) AS v`,nil},{`SELECT xmlpi(NAME foo,'   bar  ') AS v`,`<?foo bar  ?>`},
  {`SELECT xmlpi(NAME foo,'   ') AS v`,`<?foo ?>`},{`SELECT xmlpi(NAME foo,E'\t\nbar ') AS v`,"<?foo \t\nbar ?>"},
  {`SELECT xmlpi(NAME "foo$bar",42) AS v`,`<?foo_x0024_bar 42?>`},
 }{xmlBoth(t,p,b,c,xmlSelect(tc.q,"XML",tc.want))}
 xmlFailure(t,p,b,c,`SELECT xmlpi(NAME "XmL",NULL)`,xmlError("2200T","invalid XML processing instruction",`XML processing instruction target name cannot be "XmL".`,0),false)
 xmlFailure(t,p,b,c,`SELECT xmlpi(NAME foo,$1::text)`,xmlError("2200T","invalid XML processing instruction",`XML processing instruction cannot contain "?>".`,0),false,"a?>b")
 xmlBoth(t,p,b,c,xmlSelect(`SELECT xmlpi(NAME "XmL",'a?>b') AS v WHERE false`,"XML"))
})}
