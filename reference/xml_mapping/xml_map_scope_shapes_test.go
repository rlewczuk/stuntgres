package reference_test

import (
	"database/sql"
	"testing"
	"stuntgres/reference/fixture"
)

func TestReferenceXMLMapScopeShapes(t *testing.T){
	fixture.RoleReferenceCleanup(t,[]string{"xs_shapes_user"});fixture.SchemaReferenceCleanup(t,"xs_shapes")
	xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		xmlScopeSetup(t,p,c,"xs_shapes")
		xmlMapExec(t,p,c,`CREATE ROLE xs_shapes_user`,`GRANT USAGE ON SCHEMA xs_shapes TO xs_shapes_user`,`GRANT SELECT ON ALL TABLES IN SCHEMA xs_shapes TO xs_shapes_user`,`SET ROLE xs_shapes_user`)
		defer c.ExecContext(p.Ctx,`RESET ROLE`);db:=xmlMapDatabase(t,p,c)
		for _,nulls:=range []bool{false,true}{for _,forest:=range []bool{false,true}{for _,ns:=range []string{"","urn:scope",`urn:"&raw`}{
			fields:="  <a>1</a>\n";if nulls{fields+="  <b xsi:nil=\"true\"/>\n"}
			body:=xmlScopeTable("a",forest,fields)+xmlScopeTable("empty",forest)+xmlScopeTable("z",forest,"  <a>2</a>\n")
			for index,function:=range xmlScopeFunctions{
				root,schema,content:="xs_shapes","xs_shapes",body;members:=[]string{"a","empty","z"};if index>=3{root=db;schema="";members=[]string{"xs_shapes"};content=xmlScopeNested("xs_shapes",body)}
				xsd:=xmlScopeXSD(db,schema,ns,xmlMapInteger,forest,members...)
				want:=xmlScopeDocument(root,ns,"",content);if index%3==1{want=xsd};if index%3==2{want=xmlScopeDocument(root,ns,xsd,content)}
				xmlScopeBoth(t,p,b,c,xmlScopeCall(function,"xs_shapes",nulls,forest,ns),want)
			}
		}}}
	})
}
