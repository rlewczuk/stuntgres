package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
	"stuntgres/reference/fixture"
)

func TestReferenceXMLMapScopeCombined(t *testing.T){
	fixture.RoleReferenceCleanup(t,[]string{"xs_combined_user"});fixture.SchemaReferenceCleanup(t,"xs_combined")
	xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		xmlMapExec(t,p,c,`CREATE SCHEMA xs_combined`,`CREATE ROLE xs_combined_user`,`GRANT USAGE ON SCHEMA xs_combined TO xs_combined_user`,`SET ROLE xs_combined_user`)
		defer c.ExecContext(p.Ctx,`RESET ROLE`);db:=xmlMapDatabase(t,p,c)
		for _,nulls:=range []bool{false,true}{for _,forest:=range []bool{false,true}{for _,ns:=range []string{"","urn:scope"}{
			xsd:=xmlScopeXSD(db,"xs_combined",ns,"",forest)
			xmlScopeBoth(t,p,b,c,xmlScopeCall("schema_to_xml_and_xmlschema","xs_combined",nulls,forest,ns),xmlScopeDocument("xs_combined",ns,xsd,""))
			xsd=xmlScopeXSD(db,"",ns,"",forest,"xs_combined")
			xmlScopeBoth(t,p,b,c,xmlScopeCall("database_to_xml_and_xmlschema","",nulls,forest,ns),xmlScopeDocument(db,ns,xsd,xmlScopeNested("xs_combined","")))
		}}}
		xmlMapExec(t,p,c,`RESET ROLE`,`CREATE SEQUENCE xs_combined.counter`,`CREATE VIEW xs_combined.v AS SELECT nextval('xs_combined.counter')::integer AS a`,`GRANT SELECT ON xs_combined.v TO xs_combined_user`,`GRANT USAGE ON SEQUENCE xs_combined.counter TO xs_combined_user`,`SET ROLE xs_combined_user`)
		xmlScopeBoth(t,p,b,c,xmlScopeCall("schema_to_xmlschema","xs_combined",false,false,""),xmlScopeXSD(db,"xs_combined","",xmlMapInteger,false,"v"))
		xmlMapExec(t,p,c,`RESET ROLE`)
		xmlBoth(t,p,b,c,fixture.Select(`SELECT last_value,is_called FROM xs_combined.counter`,[]string{"last_value","is_called"},[]string{"INT8","BOOL"},[][]any{{int64(1),false}}))
		xmlMapExec(t,p,c,`SET ROLE xs_combined_user`)
		q:=xmlScopeCall("schema_to_xml_and_xmlschema","xs_combined",false,true,"urn:scope");stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
		for _,prepared:=range []bool{false,true}{n:=1;if prepared{n=2};xsd:=xmlScopeXSD(db,"xs_combined","urn:scope",xmlMapInteger,true,"v");step:=xmlSelect(q,"XML",xmlScopeDocument("xs_combined","urn:scope",xsd,xmlScopeTable("v",true,fmt.Sprintf("  <a>%d</a>\n",n))));if prepared{fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q},b.Recorder,step)}else{fixture.RunStep(t,p.Ctx,c,b.Recorder,step)}}
	})
}
