package reference_test

import (
	"database/sql"
	"testing"
	"stuntgres/reference/fixture"
)

func TestReferenceXMLMapScopeAuthority(t *testing.T){
	fixture.RoleReferenceCleanup(t,[]string{"xs_authority_a","xs_authority_b","xs_relation_owner","xs_definer"})
	for _,schema:=range []string{"xs_authority","xs_actor","xs_wrapper"}{fixture.SchemaReferenceCleanup(t,schema)}
	xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		xmlMapExec(t,p,c,`CREATE SCHEMA xs_authority`,`CREATE TABLE xs_authority.t(a int)`,`INSERT INTO xs_authority.t VALUES(1)`,`CREATE TABLE xs_authority.hidden(secret text)`,`CREATE ROLE xs_authority_a`,`CREATE ROLE xs_authority_b`,`GRANT USAGE ON SCHEMA xs_authority TO xs_authority_a,xs_authority_b`,`GRANT SELECT ON xs_authority.t TO xs_authority_a`,`SET ROLE xs_authority_a`)
		defer c.ExecContext(p.Ctx,`RESET ROLE`);db:=xmlMapDatabase(t,p,c)
		queries:=make([]string,6);held:=make([]*sql.Stmt,6)
		for i,function:=range xmlScopeFunctions{queries[i]=xmlScopeCall(function,"xs_authority",false,false,"");stmt,err:=c.PrepareContext(p.Ctx,queries[i]);if err!=nil{t.Fatal(err)};held[i]=stmt;defer stmt.Close()}
		phase:=func(read,usage bool,n string){
			t.Helper()
			for i:=range held{
				if i<3&&!usage{step:=fixture.ErrorDiagnostic(queries[i],xmlError("42501","permission denied for schema xs_authority","",0));step.SelectRows=true;step.ErrorStage="execute";fixture.RunStep(t,p.Ctx,c,b.Recorder,step);fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:held[i],SQL:queries[i]},b.Recorder,step);continue}
				body,types:="","";tables:=[]string{};if read&&usage{body=xmlScopeTable("t",false,"  <a>"+n+"</a>\n");types=xmlMapInteger;tables=[]string{"t"}}
				root,schema,content:="xs_authority","xs_authority",body;members:=tables;if i>=3{root=db;schema="";members=nil;content="";if usage{members=[]string{"xs_authority"};content=xmlScopeNested("xs_authority",body)}}
				xsd:=xmlScopeXSD(db,schema,"",types,false,members...);want:=xmlScopeDocument(root,"","",content);if i%3==1{want=xsd};if i%3==2{want=xmlScopeDocument(root,"",xsd,content)}
				xmlScopeHeld(t,p,b,c,held[i],queries[i],want)
			}
		}
		phase(true,true,"1")
		xmlMapExec(t,p,c,`RESET ROLE`,`REVOKE SELECT ON xs_authority.t FROM xs_authority_a`,`SET ROLE xs_authority_a`);phase(false,true,"")
		xmlMapExec(t,p,c,`RESET ROLE`,`GRANT SELECT ON xs_authority.t TO xs_authority_a`,`SET ROLE xs_authority_a`);phase(true,true,"1")
		xmlMapExec(t,p,c,`SET ROLE xs_authority_b`);phase(false,true,"")
		xmlMapExec(t,p,c,`RESET ROLE`,`REVOKE USAGE ON SCHEMA xs_authority FROM xs_authority_a`,`SET ROLE xs_authority_a`);phase(true,false,"")
		xmlMapExec(t,p,c,`RESET ROLE`,`GRANT USAGE ON SCHEMA xs_authority TO xs_authority_a`,`SET ROLE xs_authority_a`);phase(true,true,"1")
		// Same genuinely held names see a new private namespace generation.
		xmlMapExec(t,p,c,`RESET ROLE`,`BEGIN`,`DROP SCHEMA xs_authority CASCADE`,`CREATE SCHEMA xs_authority`,`CREATE TABLE xs_authority.t(a int)`,`INSERT INTO xs_authority.t VALUES(8)`,`GRANT USAGE ON SCHEMA xs_authority TO xs_authority_a`,`GRANT SELECT ON xs_authority.t TO xs_authority_a`,`SET ROLE xs_authority_a`);phase(true,true,"8")
		xmlMapExec(t,p,c,`RESET ROLE`,`ROLLBACK`,`SET ROLE xs_authority_a`);phase(true,true,"1")
		// A definer view changes relation-check ownership, not current_user's
		// expression identity. Both membership and generated SELECT/RLS must use
		// the invoker; a genuinely nested definer still owns its own expression.
		xmlMapExec(t,p,c,`RESET ROLE`,`CREATE ROLE xs_relation_owner`,`CREATE ROLE xs_definer`,`CREATE SCHEMA xs_actor`,`CREATE SCHEMA xs_wrapper`,`CREATE TABLE xs_actor.t(a int)`,`INSERT INTO xs_actor.t VALUES(1),(2)`,`ALTER TABLE xs_actor.t ENABLE ROW LEVEL SECURITY`,`CREATE POLICY xs_actor_policy ON xs_actor.t USING(a=1 AND current_user='xs_authority_a')`,`GRANT USAGE ON SCHEMA xs_actor,xs_wrapper TO xs_authority_a`,`GRANT SELECT ON xs_actor.t TO xs_authority_a`,
			`CREATE FUNCTION xs_actor.definer() RETURNS integer LANGUAGE SQL SECURITY DEFINER AS $$SELECT CASE WHEN current_user='xs_definer' THEN 5 ELSE 9 END$$`,`ALTER FUNCTION xs_actor.definer() OWNER TO xs_definer`,`CREATE VIEW xs_actor.v AS SELECT CASE WHEN current_user='xs_authority_a' THEN xs_actor.definer() ELSE 9 END AS a`,`GRANT SELECT ON xs_actor.v TO xs_authority_a`,`CREATE VIEW xs_wrapper.outer_view AS SELECT schema_to_xml('xs_actor',false,false,'') AS v`,`ALTER VIEW xs_wrapper.outer_view OWNER TO xs_relation_owner`,`GRANT SELECT ON xs_wrapper.outer_view TO xs_authority_a`,`SET ROLE xs_authority_a`)
		xmlMapExec(t,p,c,`RESET ROLE`,`CREATE TABLE xs_actor.hidden(secret text)`,`CREATE TABLE xs_actor.backing(a int)`,`INSERT INTO xs_actor.backing VALUES(4)`,`CREATE VIEW xs_actor.w AS SELECT a+10 AS a FROM xs_actor.backing`,`GRANT SELECT ON xs_actor.w TO xs_authority_a`,`CREATE VIEW xs_wrapper.outer_combined AS SELECT schema_to_xml_and_xmlschema('xs_actor',false,true,'urn:actor') AS v`,`ALTER VIEW xs_wrapper.outer_combined OWNER TO xs_relation_owner`,`GRANT SELECT ON xs_wrapper.outer_combined TO xs_authority_a`,`SET ROLE xs_authority_a`)
		body:=xmlScopeTable("t",false,"  <a>1</a>\n")+xmlScopeTable("v",false,"  <a>5</a>\n")+xmlScopeTable("w",false,"  <a>14</a>\n")
		want:=xmlScopeDocument("xs_actor","","",body)
		xmlScopeBoth(t,p,b,c,`SELECT v FROM xs_wrapper.outer_view`,want)
		forestBody:=xmlScopeTable("t",true,"  <a>1</a>\n")+xmlScopeTable("v",true,"  <a>5</a>\n")+xmlScopeTable("w",true,"  <a>14</a>\n")
		actorXSD:=xmlScopeXSD(db,"xs_actor","urn:actor",xmlMapInteger,true,"t","v","w")
		xmlScopeBoth(t,p,b,c,`SELECT v FROM xs_wrapper.outer_combined`,xmlScopeDocument("xs_actor","urn:actor",actorXSD,forestBody))
	})
}
