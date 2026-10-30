package reference_test

import (
 "database/sql"
 "strconv"
 "strings"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceHstoreKeysPrivileges(t *testing.T) {
 fixture.RoleReferenceCleanup(t,[]string{"hs_keys_actor"})
 fixture.SchemaReferenceCleanup(t,"hs_keys_schema")
 hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 hstoreInstall(t,p,b,c)
 for _,q:=range []string{"CREATE ROLE hs_keys_actor","GRANT USAGE ON SCHEMA public TO hs_keys_actor","CREATE TABLE hs_keys_acl(id integer,v hstore)","GRANT SELECT ON hs_keys_acl TO hs_keys_actor"}{hstoreRun(t,p,b,c,fixture.Exec(q,0))}
 hstoreRun(t,p,b,c,fixture.Exec("INSERT INTO hs_keys_acl VALUES (1,'a=>NULL'),(2,NULL)",2))
 for _,tc:=range []struct{name,signature,call,operator string;value any;array bool}{
 {"exist","hstore,text",`exist(v,'a')`,`v ? 'a'`,true,false},
 {"exists_all","hstore,text[]",`exists_all(v,ARRAY['a'])`,`v ?& ARRAY['a']`,true,false},
 {"exists_any","hstore,text[]",`exists_any(v,ARRAY['a'])`,`v ?| ARRAY['a']`,true,false},
 {"hs_contains","hstore,hstore",`hs_contains(v,'a=>NULL')`,`v @> 'a=>NULL'`,true,false},
 {"hs_contained","hstore,hstore",`hs_contained(v,'a=>NULL')`,`v <@ 'a=>NULL'`,true,false},
 {"akeys","hstore",`akeys(v)`,`akeys(v)`,"{a}",true},
 {"avals","hstore",`avals(v)`,`avals(v)`,"{NULL}",true},
 } {
 q:="SELECT "+tc.operator+" AS v FROM hs_keys_acl WHERE id=1"
 stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)}
 held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
 hstoreRun(t,p,b,c,fixture.Exec("REVOKE EXECUTE ON FUNCTION "+tc.name+"("+tc.signature+") FROM PUBLIC,hs_keys_actor",0))
 hstoreRun(t,p,b,c,fixture.Exec("SET ROLE hs_keys_actor",0))
 want:=fixture.ControlsReferenceDiagnostic("42501","permission denied for function "+tc.name)
 for _,form:=range []string{tc.call,tc.operator}{for _,id:=range []string{"1","2"}{hstoreError(t,p,b,c,"SELECT "+form+" FROM hs_keys_acl WHERE id="+id,false,true,false,want)}}
 hstoreError(t,p,b,held,q,false,true,false,want)
 literal:=strings.Replace(tc.operator,"v","NULL::hstore",1)
 if tc.array {literal=tc.name+"(NULL::hstore)"}
 nullQuery:="SELECT "+literal+" AS v"
 nullStep:=fixture.Select(nullQuery,[]string{"v"},[]string{"BOOL"},[][]any{{nil}})
 if tc.array {nullStep=fixture.ArrayReferenceSelect(nullQuery,[]string{"v"},[]string{"_TEXT"},[]string{"_TEXT"},[][]any{{nil}},b.PG)}
 hstoreRun(t,p,b,c,nullStep)
 hstoreRun(t,p,b,c,fixture.Exec("RESET ROLE",0));hstoreRun(t,p,b,c,fixture.Exec("GRANT EXECUTE ON FUNCTION "+tc.name+"("+tc.signature+") TO hs_keys_actor",0));hstoreRun(t,p,b,c,fixture.Exec("SET ROLE hs_keys_actor",0))
 s:=fixture.Select(q,[]string{"v"},[]string{"BOOL"},[][]any{{tc.value}})
 if tc.array{s=fixture.ArrayReferenceSelect(q,[]string{"v"},[]string{"_TEXT"},[]string{"_TEXT"},[][]any{{tc.value}},b.PG)}
 hstoreRun(t,p,b,held,s)
 hstoreRun(t,p,b,c,fixture.Exec("RESET ROLE",0));if err:=stmt.Close();err!=nil{t.Fatal(err)}
 }
 hstoreRun(t,p,b,c,fixture.Exec("DROP TABLE hs_keys_acl",0))
 hstoreRun(t,p,b,c,fixture.Exec("DROP EXTENSION hstore",0))
 hstoreRun(t,p,b,c,fixture.Exec("CREATE SCHEMA hs_keys_schema",0))
 hstoreRun(t,p,b,c,fixture.Exec("CREATE EXTENSION hstore SCHEMA hs_keys_schema",0))
 hstoreRun(t,p,b,c,fixture.Exec("CREATE TABLE hs_keys_acl(id integer,v hs_keys_schema.hstore)",0))
 hstoreRun(t,p,b,c,fixture.Exec("INSERT INTO hs_keys_acl VALUES (1,'a=>NULL'),(2,NULL)",2))
 hstoreRun(t,p,b,c,fixture.Exec("GRANT SELECT ON hs_keys_acl TO hs_keys_actor",0))
 hstoreRun(t,p,b,c,fixture.Exec("GRANT USAGE ON SCHEMA hs_keys_schema TO hs_keys_actor",0))
 for _,tc:=range []struct{form string;value any;array bool}{
 {`hs_keys_schema.exist(v,'a')`,true,false},{`hs_keys_schema.exists_all(v,ARRAY['a'])`,true,false},{`hs_keys_schema.exists_any(v,ARRAY['a'])`,true,false},
 {`hs_keys_schema.hs_contains(v,v)`,true,false},{`hs_keys_schema.hs_contained(v,v)`,true,false},{`hs_keys_schema.akeys(v)`,"{a}",true},{`hs_keys_schema.avals(v)`,"{NULL}",true},
 {`v OPERATOR(hs_keys_schema.?) 'a'`,true,false},{`v OPERATOR(hs_keys_schema.?&) ARRAY['a']`,true,false},{`v OPERATOR(hs_keys_schema.?|) ARRAY['a']`,true,false},
 {`v OPERATOR(hs_keys_schema.@>) v`,true,false},{`v OPERATOR(hs_keys_schema.<@) v`,true,false},
 } {
 q:="SELECT "+tc.form+" AS v FROM hs_keys_acl WHERE id=1"
 stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)}
 held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
 s:=fixture.Select(q,[]string{"v"},[]string{"BOOL"},[][]any{{tc.value}})
 if tc.array {s=fixture.ArrayReferenceSelect(q,[]string{"v"},[]string{"_TEXT"},[]string{"_TEXT"},[][]any{{tc.value}},b.PG)}
 hstoreRun(t,p,b,c,fixture.Exec("SET ROLE hs_keys_actor",0));hstoreRun(t,p,b,held,s)
 hstoreRun(t,p,b,c,fixture.Exec("RESET ROLE",0));hstoreRun(t,p,b,c,fixture.Exec("REVOKE USAGE ON SCHEMA hs_keys_schema FROM hs_keys_actor",0));hstoreRun(t,p,b,c,fixture.Exec("SET ROLE hs_keys_actor",0))
 want:=fixture.ControlsReferenceDiagnostic("42501","permission denied for schema hs_keys_schema")
 want.Position=strconv.Itoa(strings.Index(q,"hs_keys_schema")+1)
 if strings.Contains(tc.form," OPERATOR(") {want.Position=strconv.Itoa(strings.Index(q,"OPERATOR")+1)}
 hstoreError(t,p,b,c,q,false,true,false,want);hstoreError(t,p,b,held,q,false,true,false,want)
 hstoreRun(t,p,b,c,fixture.Exec("RESET ROLE",0));hstoreRun(t,p,b,c,fixture.Exec("GRANT USAGE ON SCHEMA hs_keys_schema TO hs_keys_actor",0));hstoreRun(t,p,b,c,fixture.Exec("SET ROLE hs_keys_actor",0))
 hstoreRun(t,p,b,held,s);hstoreRun(t,p,b,c,fixture.Exec("RESET ROLE",0))
 if err:=stmt.Close();err!=nil{t.Fatal(err)}
 }
 })
}
