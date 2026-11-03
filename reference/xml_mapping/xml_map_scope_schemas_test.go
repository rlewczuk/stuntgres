package reference_test

import (
	"database/sql"
	"testing"
	"stuntgres/reference/fixture"
)

func TestReferenceXMLMapScopeSchemas(t *testing.T){
	fixture.RoleReferenceCleanup(t,[]string{"xs_rls_user"})
	for _,schema:=range []string{"xs_schema","xs_empty","Xs.Case","xs_rls"}{fixture.SchemaReferenceCleanup(t,schema)}
	xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		xmlScopeSetup(t,p,c,"xs_schema")
		xmlMapExec(t,p,c,`CREATE SCHEMA xs_empty`,`CREATE SCHEMA "Xs.Case"`,`CREATE VIEW xs_schema.v AS SELECT a+10 AS a FROM xs_schema.z`,`CREATE MATERIALIZED VIEW xs_schema.m AS SELECT a+20 AS a FROM xs_schema.z`,`CREATE TABLE xs_schema."p.q"("x:y" int)`,`INSERT INTO xs_schema."p.q" VALUES(3)`)
		for _,forest:=range []bool{false,true}{
			body:=xmlScopeTable("a",forest,"  <a>1</a>\n")+xmlScopeTable("empty",forest)+xmlScopeTable("m",forest,"  <a>22</a>\n")+xmlScopeTable("p.q",forest,"  <x_x003A_y>3</x_x003A_y>\n")+xmlScopeTable("v",forest,"  <a>12</a>\n")+xmlScopeTable("z",forest,"  <a>2</a>\n")
			xmlScopeBoth(t,p,b,c,xmlScopeCall("schema_to_xml","xs_schema",false,forest,""),xmlScopeDocument("xs_schema","","",body))
			for _,schema:=range []string{"xs_empty","Xs.Case"}{xmlScopeBoth(t,p,b,c,xmlScopeCall("schema_to_xml",schema,false,forest,""),xmlScopeDocument(schema,"","",""))}
		}
		// Real RLS and computed view rows distinguish ordinary SELECT from raw
		// stored-row export, even when relation membership itself is unchanged.
		xmlMapExec(t,p,c,`CREATE SCHEMA xs_rls`,`CREATE TABLE xs_rls.t(a int)`,`INSERT INTO xs_rls.t VALUES(1),(2)`,`ALTER TABLE xs_rls.t ENABLE ROW LEVEL SECURITY`,`CREATE POLICY xs_policy ON xs_rls.t USING(a=2)`,`CREATE ROLE xs_rls_user`,`GRANT USAGE ON SCHEMA xs_rls TO xs_rls_user`,`GRANT SELECT ON xs_rls.t TO xs_rls_user`,`SET ROLE xs_rls_user`)
		defer c.ExecContext(p.Ctx,`RESET ROLE`)
		xmlScopeBoth(t,p,b,c,xmlScopeCall("schema_to_xml","xs_rls",false,false,""),xmlScopeDocument("xs_rls","","",xmlScopeTable("t",false,"  <a>2</a>\n")))
	})
}
