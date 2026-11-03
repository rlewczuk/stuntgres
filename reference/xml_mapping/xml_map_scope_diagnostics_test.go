package reference_test

import (
	"database/sql"
	"testing"
	"stuntgres/reference/fixture"
)

// Complete logical fields and phases are independently pinned by PostgreSQL
// 18.6 UTF8/C raw-libpq direct and PQprepare/PQexecPrepared observations.
func TestReferenceXMLMapScopeDiagnostics(t *testing.T){
	fixture.RoleReferenceCleanup(t,[]string{"xs_reader"});fixture.SchemaReferenceCleanup(t,"xs_one");fixture.SchemaReferenceCleanup(t,"xs_hidden")
	xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		for _,function:=range xmlScopeFunctions[:3]{for _,name:=range []string{"xs_absent","pg_temp"}{q:="SELECT "+function+"('"+name+"',false,false,'')";xmlFailure(t,p,b,c,q,xmlError("3F000",`schema "`+name+`" does not exist`,"",0),false)}}
		for _,tc:=range []struct{query,message string}{
			{`SELECT schema_to_xml('xs_one',false,false)`,`function schema_to_xml(unknown, boolean, boolean) does not exist`},
			{`SELECT database_to_xml(false,false)`,`function database_to_xml(boolean, boolean) does not exist`},
		}{d:=xmlError("42883",tc.message,"",8);d.Hint="No function matches the given name and argument types. You might need to add explicit type casts.";xmlFailure(t,p,b,c,tc.query,d,true)}
		xmlFailure(t,p,b,c,`SELECT schema_to_xml('xs_one','bad',false,'')`,xmlError("22P02",`invalid input syntax for type boolean: "bad"`,"",31),true)
		xmlMapExec(t,p,c,`CREATE SCHEMA xs_one`,`CREATE SCHEMA xs_hidden`,`CREATE ROLE xs_reader`,`CREATE TABLE xs_one.base(a int)`,`INSERT INTO xs_one.base VALUES(7)`,`CREATE VIEW xs_one.bad WITH(security_invoker=true) AS SELECT a FROM xs_one.base`,`GRANT USAGE ON SCHEMA xs_one TO xs_reader`,`GRANT SELECT ON xs_one.bad TO xs_reader`,`SET search_path=xs_one,public`,`SET ROLE xs_reader`)
		defer c.ExecContext(p.Ctx,`RESET ROLE`)
		for _,function:=range xmlScopeFunctions[:3]{xmlFailure(t,p,b,c,"SELECT "+function+"('xs_hidden',false,false,'')",xmlError("42501","permission denied for schema xs_hidden","",0),false)}
		d:=xmlError("42501","permission denied for table base","",0);d.Context=`SQL statement "SELECT * FROM bad"`
		for _,function:=range []string{"schema_to_xml","schema_to_xml_and_xmlschema"}{xmlFailure(t,p,b,c,"SELECT "+function+"('xs_one',false,false,'')",d,false)}
		xmlScopeBoth(t,p,b,c,`SELECT schema_to_xmlschema('xs_one',false,false,'') AS v`,xmlScopeXSD(xmlMapDatabase(t,p,c),"xs_one","",xmlMapInteger,false,"bad"))
	})
}
