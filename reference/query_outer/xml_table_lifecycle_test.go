package reference_test

import (
 "bytes"
 "database/sql"
 "strings"
 "stuntgres"
 "stuntgres/reference/fixture"
 "testing"
)

func TestReferenceXMLTableLateralPreparedAndStored(t *testing.T) {
 t.Run("lateral and outer composition",func(t *testing.T){
  resultsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
   source:=`(VALUES(1,'<r><v>7</v><v>8</v></r>'::xml),(2,'<r><v>9</v></r>'::xml),(3,'<r/>'::xml)) d(id,doc)`
   for _,lateral:=range []string{"","LATERAL "} {
    q:=`SELECT d.id,x.n,x.v FROM `+source+`, `+lateral+`XMLTABLE('/r/v' PASSING d.doc COLUMNS n FOR ORDINALITY,v int PATH '.') x ORDER BY d.id,x.n`
    fixture.SQLJSONBoth(t,p,b,c,fixture.Select(q,[]string{"id","n","v"},[]string{"INT4","INT4","INT4"},[][]any{{int64(1),int64(1),int64(7)},{int64(1),int64(2),int64(8)},{int64(2),int64(1),int64(9)}}))
   }
   q:=`SELECT d.id,x.n,x.v FROM `+source+` LEFT JOIN LATERAL XMLTABLE('/r/v' PASSING d.doc COLUMNS n FOR ORDINALITY,v int PATH '.') x ON true ORDER BY d.id,x.n`
   fixture.SQLJSONBoth(t,p,b,c,fixture.Select(q,[]string{"id","n","v"},[]string{"INT4","INT4","INT4"},[][]any{{int64(1),int64(1),int64(7)},{int64(1),int64(2),int64(8)},{int64(2),int64(1),int64(9)},{int64(3),nil,nil}}))
   fixture.SQLJSONBoth(t,p,b,c,fixture.Select(`SELECT sum(v)::bigint AS total FROM XMLTABLE('/r/v' PASSING '<r><v>7</v><v>8</v></r>' COLUMNS v int PATH '.') x WHERE v>7`,[]string{"total"},[]string{"INT8"},[][]any{{int64(8)}}))
   fixture.SQLJSONBoth(t,p,b,c,fixture.Select(`SELECT * FROM XMLTABLE(('/' || 'r') PASSING ($1::xml) COLUMNS v text PATH '.')`,[]string{"v"},[]string{"TEXT"},[][]any{{"x"}},`<r>x</r>`))
  })
 })
 t.Run("held invocation isolation",func(t *testing.T){
  resultsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
   q:=`SELECT * FROM XMLTABLE(XMLNAMESPACES($1::text AS p), ($2::text) PASSING ($3::xml) COLUMNS n FOR ORDINALITY,v int PATH $4::text DEFAULT $5::int) ORDER BY n`
   statement,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer statement.Close()
   held:=fixture.HeldStatement{Executor:c,Statement:statement,SQL:q}
   for _,tc:=range []struct{ns,row,doc,path string;fallback int;rows [][]any}{
    {"urn:a","/p:r/p:v",`<r xmlns="urn:a"><v>7</v><v/></r>`,"@missing",41,[][]any{{int64(1),int64(41)},{int64(2),int64(41)}}},
    {"urn:b","/p:r/p:w",`<r xmlns="urn:b"><w k="9"/></r>`,"@k",42,[][]any{{int64(1),int64(9)}}},
    {"urn:a","/p:r/p:v",`<r xmlns="urn:a"><v>8</v></r>`,".",43,[][]any{{int64(1),int64(8)}}},
    {"urn:a","/missing",`<r/>`,".",44,nil},
   } {
    step:=fixture.Select(q,[]string{"n","v"},[]string{"INT4","INT4"},tc.rows,tc.ns,tc.row,tc.doc,tc.path,tc.fallback)
    fixture.SQLJSONBoth(t,p,b,c,step);fixture.RunStep(t,p.Ctx,held,b.Recorder,step)
   }
  })
 })
 t.Run("stored rename and native replay",func(t *testing.T){
  resultsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
   run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
   run(fixture.Exec(`CREATE DOMAIN xt_stored_int AS int CHECK(VALUE>0)`,0))
   run(fixture.Exec(`CREATE FUNCTION xt_stored_default() RETURNS int LANGUAGE SQL VOLATILE AS 'SELECT 42'`,0))
   run(fixture.Exec(`CREATE TABLE xt_stored(ns text,doc xml,col text)`,0))
   run(fixture.Exec(`INSERT INTO xt_stored VALUES('urn:r','<r xmlns="urn:r"><v>7</v><v>8</v></r>','.')`,1))
   run(fixture.Exec(`CREATE VIEW xt_view AS SELECT x.* FROM xt_stored d, XMLTABLE(XMLNAMESPACES(d.ns AS p), ('/p:r/' || 'p:v') PASSING d.doc COLUMNS n FOR ORDINALITY,v xt_stored_int PATH d.col,fallback int PATH 'missing' DEFAULT xt_stored_default()) x`,0))
   check:=fixture.Select(`SELECT n,v::int AS v,fallback FROM xt_view ORDER BY n`,[]string{"n","v","fallback"},[]string{"INT4","INT4","INT4"},[][]any{{int64(1),int64(7),int64(42)},{int64(2),int64(8),int64(42)}})
   fixture.SQLJSONBoth(t,p,b,c,check)
   for _,q:=range []string{`ALTER DOMAIN xt_stored_int RENAME TO xt_renamed_int`,`ALTER TABLE xt_stored RENAME COLUMN doc TO payload`} {run(fixture.Exec(q,0));fixture.SQLJSONBoth(t,p,b,c,check)}
   // Exercise the actual frozen rendering by executing the returned definition.
   var definition string
   if err:=c.QueryRowContext(p.Ctx,`SELECT pg_get_viewdef('xt_view'::regclass,true)`).Scan(&definition);err!=nil {t.Fatal(err)}
   rendered:=check;rendered.Query=`SELECT n,v::int AS v,fallback FROM (`+definition+`) q ORDER BY n`
   // pg_get_viewdef includes a terminal semicolon, removed only as SQL framing.
   if len(definition)>0 && definition[len(definition)-1]==';' {rendered.Query=`SELECT n,v::int AS v,fallback FROM (`+definition[:len(definition)-1]+`) q ORDER BY n`}
   fixture.SQLJSONBoth(t,p,b,c,rendered)
   fixture.JSONPathNavigateReplay(t,p,b,c,check)
   if !b.PG {
    var before bytes.Buffer
    if err:=stuntgres.Dump(p.Ctx,p.Native,&before);err!=nil {t.Fatal(err)}
    for slot:=range 5 {
     operands:=[]string{`'urn:r'`,`'/r'`,`'<r><v>7</v></r>'`,`'.'`,`42`}
     operands[slot]="$1"
     script:=`/*é🙂*/ CREATE TABLE xt_bad_restore(v int); INSERT INTO xt_bad_restore VALUES((SELECT v FROM XMLTABLE(XMLNAMESPACES(`+operands[0]+` AS p), (`+operands[1]+`) PASSING (`+operands[2]+`) COLUMNS v int PATH `+operands[3]+` DEFAULT `+operands[4]+`)));`
     diagnostic:=fixture.ControlsReferenceDiagnostic("42P02","parameters are not allowed in restore")
     diagnostic.Position=fixture.Position(len([]rune(script[:strings.Index(script,"$1")]))+1)
     fixture.AssertDiagnostic(t,stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(script)),&diagnostic)
     var after bytes.Buffer
     if err:=stuntgres.Dump(p.Ctx,p.Native,&after);err!=nil {t.Fatal(err)}
     if !bytes.Equal(before.Bytes(),after.Bytes()) {t.Fatal("failed restore changed catalog")}
    }
   }
  })
 })
 t.Run("stored source and default assignment casts",xmlTableStoredAssignmentCasts)
 t.Run("stored DEFAULT namespace",xmlTableStoredDefaultNamespaces)
 t.Run("bare stored aliases and XML constants",xmlTableStoredAliasesAndConstants)
}

func xmlTableStoredAssignmentCasts(t *testing.T) {
 resultsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  run:=func(step fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,step)}
  for _,query:=range []string{
   `SET DateStyle TO 'ISO, MDY'`,
   `CREATE FUNCTION xt_cast_ns(integer) RETURNS text LANGUAGE SQL VOLATILE AS 'SELECT ''urn:r'''`,
   `CREATE FUNCTION xt_cast_row(integer) RETURNS text LANGUAGE SQL VOLATILE AS 'SELECT ''/p:r/p:v'''`,
   `CREATE FUNCTION xt_cast_path(integer) RETURNS text LANGUAGE SQL VOLATILE AS 'SELECT ''.'''`,
   `CREATE FUNCTION xt_cast_doc(integer) RETURNS xml LANGUAGE SQL VOLATILE AS 'SELECT XMLPARSE(DOCUMENT ''<r xmlns="urn:r"><v>7</v></r>'')'`,
   `CREATE FUNCTION xt_cast_alt_doc(integer) RETURNS xml LANGUAGE SQL VOLATILE AS 'SELECT XMLPARSE(DOCUMENT ''<r xmlns="urn:r"><v>8</v></r>'')'`,
   `CREATE FUNCTION xt_cast_date(integer) RETURNS date LANGUAGE SQL VOLATILE AS 'SELECT DATE ''2024-01-02'''`,
   `CREATE FUNCTION xt_cast_alt_date(integer) RETURNS date LANGUAGE SQL VOLATILE AS 'SELECT DATE ''2024-02-03'''`,
   `CREATE CAST (integer AS xml) WITH FUNCTION xt_cast_doc(integer) AS ASSIGNMENT`,
   `CREATE CAST (integer AS date) WITH FUNCTION xt_cast_date(integer) AS ASSIGNMENT`,
  } {run(fixture.Exec(query,0))}
  source:=func(document,fallback string)string {
   return `XMLTABLE(XMLNAMESPACES(xt_cast_ns(1) AS p), xt_cast_row(1) PASSING (`+document+`) COLUMNS n FOR ORDINALITY,v int PATH xt_cast_path(1),d date PATH 'missing' DEFAULT `+fallback+`) x`
  }
  query:=`SELECT n,v,d::text AS d FROM `+source(`$1::int`,`$2::int`)+` ORDER BY n`
  statement,err:=c.PrepareContext(p.Ctx,query);if err!=nil {t.Fatal(err)};defer statement.Close()
  held:=fixture.HeldStatement{Executor:c,Statement:statement,SQL:query}
  // Both real casts are nonSTRICT: a NULL integer still produces their value.
  for _,arguments:=range [][]any{{1,1},{nil,nil}} {
   step:=fixture.Select(query,[]string{"n","v","d"},[]string{"INT4","INT4","TEXT"},[][]any{{int64(1),int64(7),"2024-01-02"}},arguments...)
   fixture.SQLJSONBoth(t,p,b,c,step);fixture.RunStep(t,p.Ctx,held,b.Recorder,step)
  }
  run(fixture.Exec(`CREATE VIEW xt_cast_view AS SELECT * FROM `+source(`1`,`1`),0))
  check:=fixture.Select(`SELECT n,v,d::text AS d FROM xt_cast_view ORDER BY n`,[]string{"n","v","d"},[]string{"INT4","INT4","TEXT"},[][]any{{int64(1),int64(7),"2024-01-02"}})
  fixture.SQLJSONBoth(t,p,b,c,check)
  stored,err:=c.PrepareContext(p.Ctx,check.Query);if err!=nil {t.Fatal(err)};defer stored.Close()
  storedHeld:=fixture.HeldStatement{Executor:c,Statement:stored,SQL:check.Query}
  var definition string
  if err:=c.QueryRowContext(p.Ctx,`SELECT pg_get_viewdef('xt_cast_view'::regclass,true)`).Scan(&definition);err!=nil {t.Fatal(err)}
  definition=strings.TrimSuffix(strings.TrimSpace(definition),";")
  rendered:=check;rendered.Query=`SELECT n,v,d::text AS d FROM (`+definition+`) q ORDER BY n`
  fixture.SQLJSONBoth(t,p,b,c,rendered)
  for _,query:=range []string{
   `DROP CAST (integer AS xml)`,
   `CREATE CAST (integer AS xml) WITH FUNCTION xt_cast_alt_doc(integer) AS ASSIGNMENT`,
   `DROP CAST (integer AS date)`,
   `CREATE CAST (integer AS date) WITH FUNCTION xt_cast_alt_date(integer) AS ASSIGNMENT`,
  } {run(fixture.Exec(query,0))}
  fixture.SQLJSONBoth(t,p,b,c,check)
  fixture.RunStep(t,p.Ctx,storedHeld,b.Recorder,check)
  fresh:=fixture.Select(`SELECT n,v,d::text AS d FROM `+source(`1`,`1`)+` ORDER BY n`,[]string{"n","v","d"},[]string{"INT4","INT4","TEXT"},[][]any{{int64(1),int64(8),"2024-02-03"}})
  fixture.SQLJSONBoth(t,p,b,c,fresh)
  fixture.JSONPathNavigateReplay(t,p,b,c,check,fresh)
 })
}

func xmlTableStoredDefaultNamespaces(t *testing.T) {
 for _,tc:=range []struct{name,document string;empty bool}{
  {"NULL document",`NULL::xml`,true},
  {"nonNULL document",`'<r/>'::xml`,false},
 } {t.Run(tc.name,func(t *testing.T){
  resultsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
   run:=func(step fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,step)}
   source:=`SELECT * FROM XMLTABLE(XMLNAMESPACES('urn:before' AS before_ns, DEFAULT 'urn:r', 'urn:after' AS after_ns), '/r' PASSING (`+tc.document+`) COLUMNS n FOR ORDINALITY,v int PATH '.' DEFAULT 42)`
   run(fixture.Exec(`CREATE VIEW xt_default_ns_view AS `+source,0))
   check:=func(name string)fixture.Step{return fixture.Select(`SELECT n,v FROM `+name,[]string{"n","v"},[]string{"INT4","INT4"},nil)}
   read:=func(name string){
    if tc.empty {fixture.SQLJSONBoth(t,p,b,c,check(name))} else {
     fixture.SQLJSONDiagnostic(t,p,b,c,check(name).Query,xmlTableDiagnostic("0A000","DEFAULT namespace is not supported",""),false)
    }
   }
   read("xt_default_ns_view")
   var definition string
   if err:=c.QueryRowContext(p.Ctx,`SELECT pg_get_viewdef('xt_default_ns_view'::regclass,true)`).Scan(&definition);err!=nil {t.Fatal(err)}
   definition=strings.TrimSuffix(strings.TrimSpace(definition),";")
   // Recreate the actual returned SQL; do not replace or normalize namespaces.
   run(fixture.Exec(`CREATE VIEW xt_default_ns_copy AS `+definition,0))
   read("xt_default_ns_copy")
   if tc.empty {fixture.JSONPathNavigateReplay(t,p,b,c,check("xt_default_ns_view"),check("xt_default_ns_copy"))}
  })
 })}
}

func xmlTableStoredAliasesAndConstants(t *testing.T) {
 resultsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  run:=func(step fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,step)}
  run(fixture.Exec(`SET xmloption TO CONTENT`,0))
  source:=`XMLTABLE('/r' PASSING ('<r><v>7</v></r>'::xml) COLUMNS v int)`
  cases:=[]struct{name,query string;columns,types []string;rows [][]any}{
   {"xt_alias_bare",`SELECT * FROM `+source,[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}}},
   {"xt_alias_collision",`SELECT * FROM (VALUES (3)) AS "__view_function_0"(k), `+source,[]string{"k","v"},[]string{"INT4","INT4"},[][]any{{int64(3),int64(7)}}},
   {"xt_alias_columns",`SELECT renamed.answer FROM `+source+` AS renamed(answer)`,[]string{"answer"},[]string{"INT4"},[][]any{{int64(7)}}},
   {"xt_alias_wrapper",`SELECT renamed.answer FROM (SELECT * FROM `+source+`) AS renamed(answer)`,[]string{"answer"},[]string{"INT4"},[][]any{{int64(7)}}},
   {"xt_alias_capture",`SELECT v,(SELECT v+1 FROM (VALUES (1)) AS d(k)) AS next_value FROM `+source,[]string{"v","next_value"},[]string{"INT4","INT4"},[][]any{{int64(7),int64(8)}}},
   {"xt_plain_xml_constant",`SELECT '<r a="''">A&amp;B</r>'::xml AS doc`,[]string{"doc"},[]string{"XML"},[][]any{{`<r a="'">A&amp;B</r>`}}},
  }
  checks:=make([]fixture.Step,0,len(cases)*2)
  for _,tc:=range cases {
   run(fixture.Exec(`CREATE VIEW `+tc.name+` AS `+tc.query,0))
   check:=fixture.Select(`SELECT * FROM `+tc.name,tc.columns,tc.types,tc.rows)
   fixture.SQLJSONBoth(t,p,b,c,check);checks=append(checks,check)
   var definition string
   if err:=c.QueryRowContext(p.Ctx,`SELECT pg_get_viewdef($1::regclass,true)`,tc.name).Scan(&definition);err!=nil {t.Fatal(tc.name,err)}
   definition=strings.TrimSuffix(strings.TrimSpace(definition),";")
   copyQuery:=`CREATE VIEW `+tc.name+`_copy AS `+definition
   if b.PG && tc.name=="xt_alias_columns" {
    // PG18.6 ruleutils omits the XMLTABLE alias-column rename. Preserve the
    // legal original reads above and assert its broken returned SQL, rather
    // than imitating the defect or replacing the definition with hand SQL.
    // See docs/POSTGRES_REGRESSION.md; the derived-wrapper case roundtrips
    // the genuine rename on both backends.
    at:=strings.Index(copyQuery,"answer");if at<0 {t.Fatal("upstream definition lost expected answer reference",definition)}
    diagnostic:=xmlTableDiagnostic("42703",`column "answer" does not exist`,"")
    diagnostic.Position=fixture.Position(len([]rune(copyQuery[:at]))+1)
    run(fixture.ErrorDiagnostic(copyQuery,diagnostic))
    continue
   }
   run(fixture.Exec(copyQuery,0))
   copied:=check;copied.Query=`SELECT * FROM `+tc.name+`_copy`
   fixture.SQLJSONBoth(t,p,b,c,copied);checks=append(checks,copied)
  }
  fixture.JSONPathNavigateReplay(t,p,b,c,checks[0],checks[1:]...)
 })
}
