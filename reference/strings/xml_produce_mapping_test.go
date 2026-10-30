package reference_test

import (
 "database/sql"
 "fmt"
 "strings"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceXMLProduceRootDeclarations(t *testing.T){xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 for _,tc:=range []struct{q string;want any}{
  {`SELECT xmlroot('<a/>',VERSION '1.0') AS v`,`<a/>`},
  {`SELECT xmlroot('<a/>',VERSION '1.1') AS v`,`<?xml version="1.1"?><a/>`},
  {`SELECT xmlroot('<a/>',VERSION 42) AS v`,`<?xml version="42"?><a/>`},
  {`SELECT xmlroot('<a/>',VERSION '') AS v`,`<?xml version=""?><a/>`},
  {`SELECT xmlroot('<a/>',VERSION 'abc') AS v`,`<?xml version="abc"?><a/>`},
  {`SELECT xmlroot('<a/>',VERSION 'x?>junk')::text AS v`,`<?xml version="x?>junk"?><a/>`},
  {`SELECT xmlroot(NULL,VERSION '1.1') AS v`,nil},
  {`SELECT xmlroot('<?xml version="1.1" standalone="yes"?><a/>',VERSION NO VALUE) AS v`,`<?xml version="1.0" standalone="yes"?><a/>`},
  {`SELECT xmlroot('<?xml version="1.1" standalone="yes"?><a/>',VERSION NULL,STANDALONE NO VALUE) AS v`,`<a/>`},
  {`SELECT xmlroot('<?xml version="1.0" encoding="UTF8"?><a/>',VERSION '2.0',STANDALONE NO) AS v`,`<?xml version="2.0" standalone="no"?><a/>`},
  {`SELECT xmlroot(E'<?xml version="1.0"?>\n<a/>',VERSION NO VALUE)::text AS v`,"\n<a/>"},
  {`SELECT xmlroot(xmlroot('<a/>',VERSION 'x" y'),VERSION NO VALUE)::text AS v`,""},
  {`SELECT xmlroot(xmlroot('<a/>',VERSION 'x" y'),VERSION '1.1')::text AS v`,`<?xml version="1.1"?>`},
  {`SELECT xmlroot(xmlroot('<a/>',VERSION 'x" y'),VERSION NO VALUE,STANDALONE YES)::text AS v`,`<?xml version="1.0" standalone="yes"?>`},
 }{typ:="XML";if strings.Contains(tc.q,"::text AS v"){typ="TEXT"};xmlBoth(t,p,b,c,xmlSelect(tc.q,typ,tc.want))}
 // A separately specified declaration cross-product: expected versions and
 // standalone states are computed from SQL/XML's merge rule, never native rows.
 type state struct{version,standalone string}
 states:=[]state{{"",""},{"1.0",""},{"1.1",""},{"1.0","yes"},{"1.0","no"},{"1.1","yes"},{"1.1","no"}}
 declaration:=func(s state)string{if s.version==""{return ""};suffix:="";if s.standalone!=""{suffix=` standalone="`+s.standalone+`"`};return `<?xml version="`+s.version+`"`+suffix+`?>`}
 for _,left:=range states{for _,right:=range states{
  version:="";if left.version==right.version{version=left.version}
  standalone:="";if left.standalone!=""&&right.standalone!=""{standalone="yes";if left.standalone=="no"||right.standalone=="no"{standalone="no"}}
  if standalone!=""&&version==""{version="1.0"};prefix:="";if standalone!=""||version!=""&&version!="1.0"{prefix=declaration(state{version,standalone})}
  s:=xmlSelect(`SELECT xmlconcat($1::xml,NULL,$2::xml)::text AS v`,"TEXT",prefix+`<a/><b/>`);s.Args=[]any{declaration(left)+`<a/>`,declaration(right)+`<b/>`};xmlBoth(t,p,b,c,s)
 }}
 for _,source:=range states{for _,version:=range []any{nil,"1.0","1.1","","2.0"}{for _,option:=range []string{"",",STANDALONE NO VALUE",",STANDALONE YES",",STANDALONE NO"}{
  standalone:=source.standalone
  switch option{case ",STANDALONE NO VALUE":standalone="";case ",STANDALONE YES":standalone="yes";case ",STANDALONE NO":standalone="no"}
  spelling:="";present:=version!=nil;if present{spelling=version.(string)}
  if !present&&standalone!=""{spelling="1.0";present=true}
  prefix:="";if present&&(spelling!="1.0"||standalone!=""){prefix=`<?xml version="`+spelling+`"`;if standalone!=""{prefix+=` standalone="`+standalone+`"`};prefix+=`?>`}
  step:=xmlSelect(`SELECT xmlroot($1::xml,VERSION $2::text`+option+`)::text AS v`,"TEXT",prefix+`<a/>`);step.Args=[]any{declaration(source)+`<a/>`,version};xmlBoth(t,p,b,c,step)
 }}}
})}

func TestReferenceXMLProduceValueMapping(t *testing.T){xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 for _,q:=range []string{`CREATE DOMAIN xml_produce_bool AS boolean`,`CREATE DOMAIN xml_produce_array AS integer[]`,`CREATE DOMAIN xml_produce_xml AS xml`}{if _,err:=c.ExecContext(p.Ctx,q);err!=nil{t.Fatal(err)}}
 for _,tc:=range []struct{q,want string}{
  {`SELECT xmlforest(true AS a,false AS b) AS v`,`<a>true</a><b>false</b>`},
  {`SELECT xmlelement(NAME r,ARRAY[[1,NULL],[3,4]]) AS v`,`<r><element>1</element><element>3</element><element>4</element></r>`},
  {`SELECT xmlelement(NAME r,ARRAY[]::integer[]) AS v`,`<r></r>`},
  {`SELECT xmlelement(NAME r,ARRAY[NULL,NULL]::integer[]) AS v`,`<r></r>`},
  {`SELECT xmlelement(NAME r,XMLATTRIBUTES(ARRAY[1,NULL,3] AS a)) AS v`,`<r a="&lt;element&gt;1&lt;/element&gt;&lt;element&gt;3&lt;/element&gt;"/>`},
  {`SELECT xmlforest(true::xml_produce_bool AS b,ARRAY[2,NULL,4]::xml_produce_array AS a,'<x/>'::xml_produce_xml AS x) AS v`,`<b>true</b><a><element>2</element><element>4</element></a><x><x/></x>`},
  {`SELECT xmlforest(DATE '2020-02-03' AS d,TIMESTAMP '2020-02-03 04:05:06.120' AS t,TIMESTAMPTZ '2020-02-03 04:05:06+00' AS z) AS v`,`<d>2020-02-03</d><t>2020-02-03T04:05:06.12</t><z>2020-02-03T04:05:06+00:00</z>`},
  {`SELECT xmlforest(DATE '0001-01-02 BC' AS d,TIMESTAMP '0001-01-02 03:04:05 BC' AS t,TIMESTAMPTZ '0001-01-02 03:04:05+00 BC' AS z) AS v`,`<d>0001-01-02 BC</d><t>0001-01-02T03:04:05 BC</t><z>0001-01-02T03:04:05+00:00 BC</z>`},
 }{xmlBoth(t,p,b,c,xmlSelect(tc.q,"XML",tc.want))}
 for _,n:=range []int{0,1,54,55,108,109}{
  want:=strings.Repeat("/",n/3*4);if n%3==1{want+="/w=="}else if n%3==2{want+="//8="}
  chunks:=[]string{};for len(want)>72{chunks=append(chunks,want[:72]);want=want[72:]};chunks=append(chunks,want);want=strings.Join(chunks,"\r\n")
  xmlBoth(t,p,b,c,xmlSelect(fmt.Sprintf(`SELECT xmlelement(NAME r,decode(repeat('ff',%d),'hex')) AS v`,n),"XML","<r>"+want+"</r>"))
 }
 for _,typ:=range []string{"date","timestamp","timestamptz"}{kind:="timestamp";if typ=="date"{kind="date"};xmlFailure(t,p,b,c,`SELECT xmlelement(NAME r,'infinity'::`+typ+`)`,xmlError("22008",kind+" out of range","XML does not support infinite "+kind+" values.",0),false)}
})}

func TestReferenceXMLProduceSettings(t *testing.T){xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 if _,err:=c.ExecContext(p.Ctx,`SET DateStyle='German, DMY'`);err!=nil{t.Fatal(err)}
 if _,err:=c.ExecContext(p.Ctx,`SET bytea_output='escape'`);err!=nil{t.Fatal(err)}
 xmlBoth(t,p,b,c,xmlSelect(`SELECT xmlelement(NAME d,DATE '2020-02-03') AS v`,"XML",`<d>2020-02-03</d>`))
 for _,q:=range []string{`SELECT xmlelement(NAME r,decode('abcdef','hex')) AS v`,`SELECT xmlelement(NAME r,$1::bytea) AS v`}{
  stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)}
  for i,setting:=range []string{"base64","hex","base64"}{if _,err:=c.ExecContext(p.Ctx,`SET xmlbinary='`+setting+`'`);err!=nil{t.Fatal(err)};want:=`<r>q83v</r>`;if i==1{want=`<r>ABCDEF</r>`};step:=xmlSelect(q,"XML",want);if strings.Contains(q,"$1"){step.Args=[]any{[]byte{0xab,0xcd,0xef}}};fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q},b.Recorder,step)}
  stmt.Close()
 }
 for _,q:=range []string{`SELECT xmlelement(NAME z,TIMESTAMPTZ '2020-02-03 04:05:06+00') AS v`,`SELECT xmlforest($1::timestamptz AS z) AS v`}{
  stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)}
  for i,zone:=range []string{"UTC","Asia/Kolkata","UTC"}{if _,err:=c.ExecContext(p.Ctx,`SET timezone='`+zone+`'`);err!=nil{t.Fatal(err)};want:=`<z>2020-02-03T04:05:06+00:00</z>`;if i==1{want=`<z>2020-02-03T09:35:06+05:30</z>`};step:=xmlSelect(q,"XML",want);if strings.Contains(q,"$1"){step.Args=[]any{"2020-02-03 04:05:06+00"}};fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q},b.Recorder,step)}
  stmt.Close()
 }
 if _,err:=c.ExecContext(p.Ctx,`BEGIN`);err!=nil{t.Fatal(err)}
 for _,tc:=range []struct{name,sql string;settings,want []string}{
  {"xml_produce_binary",`SELECT xmlelement(NAME r,decode('abcdef','hex')) AS v FROM generate_series(1,3)`,[]string{`SET xmlbinary='base64'`,`SET xmlbinary='hex'`,`SET xmlbinary='base64'`},[]string{`<r>q83v</r>`,`<r>ABCDEF</r>`,`<r>q83v</r>`}},
  {"xml_produce_zone",`SELECT xmlelement(NAME z,TIMESTAMPTZ '2020-02-03 04:05:06+00') AS v FROM generate_series(1,3)`,[]string{`SET timezone='UTC'`,`SET timezone='Asia/Kolkata'`,`SET timezone='UTC'`},[]string{`<z>2020-02-03T04:05:06+00:00</z>`,`<z>2020-02-03T09:35:06+05:30</z>`,`<z>2020-02-03T04:05:06+00:00</z>`}},
 }{if _,err:=c.ExecContext(p.Ctx,`DECLARE `+tc.name+` NO SCROLL CURSOR FOR `+tc.sql);err!=nil{t.Fatal(err)};for i,setting:=range tc.settings{if _,err:=c.ExecContext(p.Ctx,setting);err!=nil{t.Fatal(err)};fixture.RunStep(t,p.Ctx,c,b.Recorder,xmlSelect(`FETCH 1 FROM `+tc.name,"XML",tc.want[i]))};if _,err:=c.ExecContext(p.Ctx,`CLOSE `+tc.name);err!=nil{t.Fatal(err)}}
 if _,err:=c.ExecContext(p.Ctx,`COMMIT`);err!=nil{t.Fatal(err)}
})}
