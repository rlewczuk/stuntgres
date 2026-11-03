package reference_test

import (
	"database/sql"
	"testing"
	"stuntgres/reference/fixture"
)

func TestReferenceXMLMapScopePrivileges(t *testing.T){
	fixture.RoleReferenceCleanup(t,[]string{"xs_acl_user","xs_acl_group"})
	fixture.SchemaReferenceCleanup(t,"xs_acl");fixture.SchemaReferenceCleanup(t,"xs_hidden")
	xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		xmlMapExec(t,p,c,`CREATE SCHEMA xs_acl`,`CREATE SCHEMA xs_hidden`,`CREATE ROLE xs_acl_user`,`CREATE ROLE xs_acl_group`,`GRANT xs_acl_group TO xs_acl_user`,`GRANT USAGE ON SCHEMA xs_acl TO xs_acl_user`,
			`CREATE TABLE xs_acl.denied(secret text)`,`CREATE TABLE xs_acl.column_one(secret text,b int)`,`CREATE TABLE xs_acl.column_all(secret text,b int)`,`GRANT SELECT(secret) ON xs_acl.column_one TO xs_acl_user`,`GRANT SELECT(secret,b) ON xs_acl.column_all TO xs_acl_user`,
			`CREATE TABLE xs_acl.inherited(a int)`,`INSERT INTO xs_acl.inherited VALUES(3)`,`GRANT SELECT ON xs_acl.inherited TO xs_acl_group`,`CREATE TABLE xs_acl.readable(a int)`,`INSERT INTO xs_acl.readable VALUES(4)`,`GRANT SELECT ON xs_acl.readable TO xs_acl_user`,
			`CREATE TABLE xs_acl.owner_revoked(secret text)`,`ALTER TABLE xs_acl.owner_revoked OWNER TO xs_acl_user`,`REVOKE SELECT ON xs_acl.owner_revoked FROM xs_acl_user`,`CREATE TABLE xs_hidden.hidden(secret text)`,`GRANT SELECT ON xs_hidden.hidden TO xs_acl_user`,`SET ROLE xs_acl_user`)
		defer c.ExecContext(p.Ctx,`RESET ROLE`);db:=xmlMapDatabase(t,p,c)
		for index,function:=range xmlScopeFunctions{
			body:=xmlScopeTable("inherited",false,"  <a>3</a>\n")+xmlScopeTable("readable",false,"  <a>4</a>\n");root,schema,content:="xs_acl","xs_acl",body;members:=[]string{"inherited","readable"};if index>=3{root=db;schema="";members=[]string{"xs_acl"};content=xmlScopeNested("xs_acl",body)}
			xsd:=xmlScopeXSD(db,schema,"",xmlMapInteger,false,members...);want:=xmlScopeDocument(root,"","",content);if index%3==1{want=xsd};if index%3==2{want=xmlScopeDocument(root,"",xsd,content)}
			xmlScopeBoth(t,p,b,c,xmlScopeCall(function,"xs_acl",false,false,""),want)
			if index<3{xmlFailure(t,p,b,c,xmlScopeCall(function,"xs_hidden",false,false,""),xmlError("42501","permission denied for schema xs_hidden","",0),false)}
		}
	})
}
