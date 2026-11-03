package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"
	"stuntgres/reference/fixture"
)

func TestReferenceXMLMapScopeKinds(t *testing.T){
	fixture.RoleReferenceCleanup(t,[]string{"xs_kinds_user"});fixture.SchemaReferenceCleanup(t,"xs_kinds")
	xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		defer func(){
			ctx,cancel:=context.WithTimeout(context.Background(),15*time.Second);defer cancel()
			if _,err:=c.ExecContext(ctx,`RESET ROLE`);err!=nil{t.Error("foreign fixture actor cleanup",err)}
			if _,err:=c.ExecContext(ctx,`DROP FOREIGN DATA WRAPPER IF EXISTS xs_scope_fdw CASCADE`);err!=nil{t.Error("foreign fixture cleanup",err)}
		}()
		xmlMapExec(t,p,c,`CREATE SCHEMA xs_kinds`,`CREATE TABLE xs_kinds.root(a int) PARTITION BY RANGE(a)`,`CREATE TABLE xs_kinds.child PARTITION OF xs_kinds.root FOR VALUES FROM(0) TO(10)`,`INSERT INTO xs_kinds.root VALUES(1)`,`CREATE SEQUENCE xs_kinds.seq`,`CREATE INDEX xs_kind_index ON xs_kinds.child(a)`,`CREATE TYPE xs_kinds.composite AS(a int)`,`CREATE FOREIGN DATA WRAPPER xs_scope_fdw`,`CREATE SERVER xs_scope_server FOREIGN DATA WRAPPER xs_scope_fdw`,`CREATE FOREIGN TABLE xs_kinds.foreign_shell(a int) SERVER xs_scope_server`,`CREATE MATERIALIZED VIEW xs_kinds.mat AS SELECT 2 AS a`,`CREATE VIEW xs_kinds.v AS SELECT 3 AS a`,`CREATE ROLE xs_kinds_user`,`GRANT USAGE ON SCHEMA xs_kinds TO xs_kinds_user`,`GRANT SELECT ON ALL TABLES IN SCHEMA xs_kinds TO xs_kinds_user`)
		// Explicit system schemas are not subjected to database's exclusion.
		xmlBoth(t,p,b,c,xmlSelect(`SELECT position('name="pg_class" type="TableType.' IN schema_to_xmlschema('pg_catalog',false,false,'')::text)>0 AS v`,"BOOL",true))
		xmlMapExec(t,p,c,`SET ROLE xs_kinds_user`,`CREATE TEMP TABLE xs_temp_t(a int)`,`INSERT INTO xs_temp_t VALUES(4)`)
		var temp string;if err:=c.QueryRowContext(p.Ctx,`SELECT nspname::text FROM pg_namespace WHERE oid=pg_my_temp_schema()`).Scan(&temp);err!=nil{t.Fatal(err)}
		xmlScopeBoth(t,p,b,c,xmlScopeCall("schema_to_xml","pg_temp",false,false,""),xmlScopeDocument(temp,"","",xmlScopeTable("xs_temp_t",false,"  <a>4</a>\n")))
		db:=xmlMapDatabase(t,p,c);body:=xmlScopeTable("child",false,"  <a>1</a>\n")+xmlScopeTable("mat",false,"  <a>2</a>\n")+xmlScopeTable("v",false,"  <a>3</a>\n")
		for index,function:=range xmlScopeFunctions{
			root,schema,content:="xs_kinds","xs_kinds",body;members:=[]string{"child","mat","v"};if index>=3{root=db;schema="";members=[]string{"xs_kinds"};content=xmlScopeNested("xs_kinds",body)}
			xsd:=xmlScopeXSD(db,schema,"",xmlMapInteger,false,members...);want:=xmlScopeDocument(root,"","",content);if index%3==1{want=xsd};if index%3==2{want=xmlScopeDocument(root,"",xsd,content)}
			xmlScopeBoth(t,p,b,c,xmlScopeCall(function,"xs_kinds",false,false,""),want)
		}
	})
}
