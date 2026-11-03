package reference_test

import (
	"database/sql"
	"testing"
	"stuntgres/reference/fixture"
)

func TestReferenceXMLMapScopeSmoke(t *testing.T){
	fixture.RoleReferenceCleanup(t,[]string{"xs_smoke_user"});for _,schema:=range []string{"xs_smoke","xs_smoke_empty","xs_smoke_hidden"}{fixture.SchemaReferenceCleanup(t,schema)}
	xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		xmlMapExec(t,p,c,`CREATE SCHEMA xs_smoke`,`CREATE TABLE xs_smoke.t_b(a int)`,`INSERT INTO xs_smoke.t_b VALUES(2)`,`CREATE TABLE xs_smoke.t_a(a int,b int)`,`INSERT INTO xs_smoke.t_a VALUES(1,NULL)`,`CREATE SCHEMA xs_smoke_empty`,`CREATE SCHEMA xs_smoke_hidden`,`CREATE TABLE xs_smoke_hidden.secret(hidden text)`,`CREATE ROLE xs_smoke_user`,`GRANT USAGE ON SCHEMA xs_smoke,xs_smoke_empty TO xs_smoke_user`,`GRANT SELECT ON ALL TABLES IN SCHEMA xs_smoke,xs_smoke_hidden TO xs_smoke_user`,`SET ROLE xs_smoke_user`)
		defer c.ExecContext(p.Ctx,`RESET ROLE`);db:=xmlMapDatabase(t,p,c)
		for i,tc:=range []struct{nulls,forest bool;ns string}{{false,false,""},{false,true,"urn:scope"},{true,true,"urn:scope"},{true,false,""},{false,false,""},{false,true,"urn:scope"}}{
			fields:="  <a>1</a>\n";if tc.nulls{fields+="  <b xsi:nil=\"true\"/>\n"};body:=xmlScopeTable("t_a",tc.forest,fields)+xmlScopeTable("t_b",tc.forest,"  <a>2</a>\n")
			root,schema,content:="xs_smoke","xs_smoke",body;members:=[]string{"t_a","t_b"};if i>=3{root=db;schema="";content=xmlScopeNested("xs_smoke",body)+xmlScopeNested("xs_smoke_empty","");members=[]string{"xs_smoke","xs_smoke_empty"}}
			xsd:=xmlScopeXSD(db,schema,tc.ns,xmlMapInteger,tc.forest,members...);want:=xmlScopeDocument(root,tc.ns,"",content);if i%3==1{want=xsd};if i%3==2{want=xmlScopeDocument(root,tc.ns,xsd,content)}
			xmlScopeBoth(t,p,b,c,xmlScopeCall(xmlScopeFunctions[i],"xs_smoke",tc.nulls,tc.forest,tc.ns),want)
		}
		q:=xmlScopeCall("schema_to_xml","xs_smoke",false,false,"");held,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer held.Close()
		dbq:=xmlScopeCall("database_to_xml","",false,false,"");dbheld,err:=c.PrepareContext(p.Ctx,dbq);if err!=nil{t.Fatal(err)};defer dbheld.Close()
		all:=xmlScopeTable("t_a",false,"  <a>1</a>\n")+xmlScopeTable("t_b",false,"  <a>2</a>\n")
		xmlScopeHeld(t,p,b,c,held,q,xmlScopeDocument("xs_smoke","","",all))
		xmlMapExec(t,p,c,`RESET ROLE`,`REVOKE SELECT ON xs_smoke.t_a FROM xs_smoke_user`,`SET ROLE xs_smoke_user`)
		xmlScopeHeld(t,p,b,c,held,q,xmlScopeDocument("xs_smoke","","",xmlScopeTable("t_b",false,"  <a>2</a>\n")))
		xmlMapExec(t,p,c,`RESET ROLE`,`GRANT SELECT ON xs_smoke.t_a TO xs_smoke_user`,`SET ROLE xs_smoke_user`)
		xmlScopeHeld(t,p,b,c,held,q,xmlScopeDocument("xs_smoke","","",all))
		xmlMapExec(t,p,c,`RESET ROLE`,`REVOKE USAGE ON SCHEMA xs_smoke FROM xs_smoke_user`,`SET ROLE xs_smoke_user`)
		xmlScopeHeld(t,p,b,c,dbheld,dbq,xmlScopeDocument(db,"","",xmlScopeNested("xs_smoke_empty","")))
		step:=fixture.ErrorDiagnostic(q,xmlError("42501","permission denied for schema xs_smoke","",0));step.SelectRows=true;step.ErrorStage="execute";fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:held,SQL:q},b.Recorder,step);xmlFailure(t,p,b,c,q,*step.Diagnostic,false)
		xmlMapExec(t,p,c,`RESET ROLE`,`GRANT USAGE ON SCHEMA xs_smoke TO xs_smoke_user`,`SET ROLE xs_smoke_user`)
		xmlScopeHeld(t,p,b,c,held,q,xmlScopeDocument("xs_smoke","","",all));xmlScopeHeld(t,p,b,c,dbheld,dbq,xmlScopeDocument(db,"","",xmlScopeNested("xs_smoke",all)+xmlScopeNested("xs_smoke_empty","")))
	})
}
