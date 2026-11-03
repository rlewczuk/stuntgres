package reference_test

import (
	"database/sql"
	"testing"
	"stuntgres/reference/fixture"
)

func TestReferenceXMLMapScopeDatabases(t *testing.T){
	fixture.RoleReferenceCleanup(t,[]string{"xs_db_user"})
	for _,schema:=range []string{"xs_db_a","xs_db_z","xs_db_empty","xs_db_hidden"}{fixture.SchemaReferenceCleanup(t,schema)}
	xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		xmlMapExec(t,p,c,`CREATE SCHEMA xs_db_z`,`CREATE TABLE xs_db_z.t(a int)`,`INSERT INTO xs_db_z.t VALUES(2)`,`CREATE SCHEMA xs_db_a`,`CREATE TABLE xs_db_a.t(a int)`,`INSERT INTO xs_db_a.t VALUES(1)`,`CREATE SCHEMA xs_db_empty`,`CREATE SCHEMA xs_db_hidden`,`CREATE TABLE xs_db_hidden.secret(a int)`,`INSERT INTO xs_db_hidden.secret VALUES(9)`,`CREATE ROLE xs_db_user`,`GRANT SELECT ON xs_db_hidden.secret TO xs_db_user`,`SET ROLE xs_db_user`)
		defer c.ExecContext(p.Ctx,`RESET ROLE`);db:=xmlMapDatabase(t,p,c)
		for index,function:=range xmlScopeFunctions[3:]{xsd:=xmlScopeXSD(db,"","","",false);want:=xmlScopeDocument(db,"","","");if index==1{want=xsd};if index==2{want=xmlScopeDocument(db,"",xsd,"")};xmlScopeBoth(t,p,b,c,xmlScopeCall(function,"",false,false,""),want)}
		xmlMapExec(t,p,c,`RESET ROLE`,`GRANT USAGE ON SCHEMA xs_db_a,xs_db_z,xs_db_empty,public TO xs_db_user`,`GRANT SELECT ON xs_db_a.t,xs_db_z.t TO xs_db_user`,`SET ROLE xs_db_user`)
		for _,forest:=range []bool{false,true}{
			body:=xmlScopeNested("public","")+xmlScopeNested("xs_db_a",xmlScopeTable("t",forest,"  <a>1</a>\n"))+xmlScopeNested("xs_db_empty","")+xmlScopeNested("xs_db_z",xmlScopeTable("t",forest,"  <a>2</a>\n"))
			for index,function:=range xmlScopeFunctions[3:]{
				xsd:=xmlScopeXSD(db,"","",xmlMapInteger,forest,"public","xs_db_a","xs_db_empty","xs_db_z");want:=xmlScopeDocument(db,"","",body);if index==1{want=xsd};if index==2{want=xmlScopeDocument(db,"",xsd,body)}
				for _,path:=range []string{`SET search_path=xs_db_z`,`SET search_path=public`}{xmlMapExec(t,p,c,path);xmlScopeBoth(t,p,b,c,xmlScopeCall(function,"",false,forest,""),want)}
			}
		}
	})
}
