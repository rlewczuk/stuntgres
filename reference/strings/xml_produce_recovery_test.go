package reference_test

import (
 "database/sql"
 "fmt"
 "strings"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceXMLProduceContextualIdentifiers(t *testing.T){xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 for _,tc:=range []struct{name,text,fragment string}{{"content","x","<c/>"},{"document","d","<d/>"},{"preserve","p","<p/>"},{"strip","s","<s/>"},{"whitespace","w","<w/>"}}{
  xmlBoth(t,p,b,c,xmlSelect(`SELECT xmlforest(`+tc.name+`) AS v FROM (SELECT '`+tc.text+`'::text AS `+tc.name+`)s`,"XML","<"+tc.name+">"+tc.text+"</"+tc.name+">"))
  for _,head:=range []string{"xmlconcat(","xmlroot("}{tail:=")";if head=="xmlroot("{tail=",VERSION '1.0')"};xmlBoth(t,p,b,c,xmlSelect(`SELECT `+head+tc.name+tail+` AS v FROM (SELECT '`+tc.fragment+`'::xml AS `+tc.name+`)s`,"XML",tc.fragment))}
 }
 for _,tc:=range []struct{q,typ,want string}{
  {`SELECT xmlforest(content) AS v FROM (SELECT 'x'::text AS content) AS s`,"XML",`<content>x</content>`},
  {`SELECT xmlconcat(document) AS v FROM (SELECT '<a/>'::xml AS document) AS s`,"XML",`<a/>`},
  {`SELECT xmlroot(document,VERSION '1.0') AS v FROM (SELECT '<a/>'::xml AS document) AS s`,"XML",`<a/>`},
  {`SELECT xmlforest(preserve AS whitespace,strip AS content) AS v FROM (SELECT 'p'::text AS preserve,'s'::text AS strip)s`,"XML",`<whitespace>p</whitespace><content>s</content>`},
  {`SELECT xmlparse(CONTENT content PRESERVE WHITESPACE) AS v FROM (SELECT '<c>  </c>'::text AS content)s`,"XML",`<c>  </c>`},
  {`SELECT xmlparse(DOCUMENT document STRIP WHITESPACE) AS v FROM (SELECT '<d/>'::text AS document)s`,"XML",`<d/>`},
  {`SELECT xmlserialize(CONTENT xmlconcat(document::xml) AS text) AS v FROM (SELECT '<d/>'::text AS document)s`,"TEXT",`<d/>`},
  {`SELECT xmlserialize(DOCUMENT xmlroot(document::xml,VERSION '1.0') AS text) AS v FROM (SELECT '<d/>'::text AS document)s`,"TEXT",`<d/>`},
  {`SELECT xmlroot(xmlconcat(document::xml,xmlparse(CONTENT content PRESERVE WHITESPACE)),VERSION '1.0') AS v FROM (SELECT '<c>  </c>'::text AS content,'<d/>'::text AS document)s`,"XML",`<d/><c>  </c>`},
  {`SELECT xmlforest(xmlparse(CONTENT content STRIP WHITESPACE) AS preserve) AS v FROM (SELECT '<c>  </c>'::text AS content)s`,"XML",`<preserve><c>  </c></preserve>`},
  {`SELECT x.v FROM (SELECT 'x'::text AS content)s CROSS JOIN LATERAL XMLTABLE('/r' PASSING xmlelement(NAME r,XMLATTRIBUTES('v' AS content),xmlforest(content)) COLUMNS v text PATH '@content')x`,"TEXT",`v`},
 }{xmlBoth(t,p,b,c,xmlSelect(tc.q,tc.typ,tc.want))}
})}

func TestReferenceXMLProduceNameReplay(t *testing.T){xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 for i,tc:=range []struct{name,inferred,explicit,replay string}{
  {"t","t","t","t"},
  {"_x0041_","_x005F_x0041_","_x005F_x0041_","_x005F_x0041_"},
  {":lead","_x003A_lead","_x003A_lead","_x003A_lead"},
  {"xmlThing","_x0078_mlThing","xmlThing","xmlThing"},
  {"a:b","a_x003A_b","a:b","a:b"},
  {"😀","_x1F600_","_x1F600_","_x005F_x1F600_"},
  {"a\u200cb","a_x200C_b","a_x200C_b","a_x200C_b"},
  {"xml","_x0078_ml","xml","xml"},
  {"XML","_x0058_ML","XML","XML"},
 }{
  for _,inferred:=range []bool{true,false}{tag:=tc.explicit;if inferred{tag=tc.inferred};xmlProduceNameReplay(t,p,b,c,fmt.Sprintf("xml_recovery_name_%d_%t",i,inferred),tc.name,inferred,tag,tc.replay)}
 }
})}

// The original held plan retains accepted names; presentation replay follows the
// release inverse, including its xml-prefix, colon and supplementary differences.
func xmlProduceNameReplay(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,prefix,label string,inferred bool,original,replayed string){
 t.Helper()
 quote:=func(s string)string{return `"`+strings.ReplaceAll(s,`"`,`""`)+`"`}
 source:=label;if !inferred{source="s"}
 table,view:=prefix+"_source",prefix+"_view"
 operand:=quote(source);if !inferred{operand+=" AS "+quote(label)}
 for _,step:=range []fixture.Step{
  fixture.Exec(`CREATE TABLE `+table+`(`+quote(source)+` text)`,0),
  fixture.Exec(`INSERT INTO `+table+` VALUES('x')`,1),
  fixture.Exec(`CREATE VIEW `+view+` AS SELECT xmlforest(`+operand+`) AS f,xmlelement(NAME r,XMLATTRIBUTES(`+operand+`)) AS e FROM `+table,0),
 }{fixture.RunStep(t,p.Ctx,c,b.Recorder,step)}
 expected:=func(name,tag string)fixture.Step{return fixture.Select(`SELECT f,e FROM `+name,[]string{"f","e"},[]string{"XML","XML"},[][]any{{"<"+tag+">x</"+tag+">",`<r `+tag+`="x"/>`}})}
 want:=expected(view,original);xmlBoth(t,p,b,c,want)
 held,err:=c.PrepareContext(p.Ctx,want.Query);if err!=nil{t.Fatal(err)};defer held.Close()
 executeHeld:=func(){t.Helper();fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:held,SQL:want.Query},b.Recorder,want)}
 executeHeld()
 var replays []string
 replay:=func(stage string){t.Helper();for _,pretty:=range []bool{false,true}{
  var definition string
  if err:=c.QueryRowContext(p.Ctx,fmt.Sprintf(`SELECT pg_get_viewdef('%s'::regclass,%t)`,view,pretty)).Scan(&definition);err!=nil{t.Fatal(err)}
  name:=fmt.Sprintf("%s_%s_%t",prefix,stage,pretty)
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(`CREATE VIEW `+name+` AS `+definition,0))
  xmlBoth(t,p,b,c,expected(name,replayed));replays=append(replays,name)
 }}
 replay("before")
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(`ALTER TABLE `+table+` RENAME COLUMN `+quote(source)+` TO renamed`,0))
 executeHeld();xmlBoth(t,p,b,c,want)
 for _,name:=range replays{xmlBoth(t,p,b,c,expected(name,replayed))}
 replay("after")
}
