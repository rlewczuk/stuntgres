package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"stuntgres/reference/fixture"
)

func TestReferenceXMLMapScopeXSD(t *testing.T){
	fixture.RoleReferenceCleanup(t,[]string{"xs_xsd_user"});for _,schema:=range []string{"xs_xsd","xs_xsd_empty"}{fixture.SchemaReferenceCleanup(t,schema)}
	xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		xmlMapExec(t,p,c,`CREATE SCHEMA xs_xsd`,`CREATE SCHEMA xs_xsd_empty`,`CREATE DOMAIN xs_xsd.base AS int`,`CREATE DOMAIN xs_xsd.nested AS xs_xsd.base`,`CREATE TABLE xs_xsd.z(a xs_xsd.nested,b varchar(3))`,`CREATE TABLE xs_xsd.a(a xs_xsd.nested,b varchar(9),dropped text)`,`ALTER TABLE xs_xsd.a DROP COLUMN dropped`,`CREATE ROLE xs_xsd_user`,`GRANT USAGE ON SCHEMA xs_xsd,xs_xsd_empty TO xs_xsd_user`,`GRANT SELECT ON ALL TABLES IN SCHEMA xs_xsd TO xs_xsd_user`,`SET ROLE xs_xsd_user`)
		defer c.ExecContext(p.Ctx,`RESET ROLE`);db:=xmlMapDatabase(t,p,c)
		// SQL/XML fully escapes an underscore starting "_x", independently of
		// multipart period escaping. These are raw-SQL xs_xsd/xs_xsd_empty.
		const schemaXML,emptyXML="xs_x005F_xsd","xs_x005F_xsd_empty"
		domain:=`<xsd:simpleType name="Domain.`+db+`.`+schemaXML+`.nested">`+"\n  <xsd:restriction base=\"INTEGER\"/>\n</xsd:simpleType>\n\n"
		varchar:="<xsd:simpleType name=\"VARCHAR\">\n  <xsd:restriction base=\"xsd:string\">\n  </xsd:restriction>\n</xsd:simpleType>\n\n"
		// Database scalar collection has no released ORDER BY. Require every
		// exact definition once and compare the entire remainder, not parity or
		// an invented type order. Schema uses the same independent check.
		for _,nulls:=range []bool{false,true}{for _,forest:=range []bool{false,true}{for _,function:=range []string{"schema_to_xmlschema","database_to_xmlschema"}{
			q:=xmlScopeCall(function,"xs_xsd",nulls,forest,"");stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)}
			for _,prepared:=range []bool{false,true}{
				var rows *sql.Rows;if prepared{rows,err=stmt.QueryContext(p.Ctx)}else{rows,err=c.QueryContext(p.Ctx,q)};if err!=nil{stmt.Close();t.Fatal(err)}
				xmlMetadata(t,rows,b.PG);if !rows.Next(){rows.Close();stmt.Close();t.Fatal("missing XSD",rows.Err())};var got string;if err:=rows.Scan(&got);err!=nil{t.Fatal(err)};if rows.Next()||rows.Err()!=nil{t.Fatal("extra XSD rows",rows.Err())};rows.Close()
				for _,definition:=range []string{domain,varchar,xmlMapInteger}{if strings.Count(got,definition)!=1{t.Fatalf("missing/duplicate released scalar definition %q in %s",definition,got)};got=strings.Replace(got,definition,"",1)}
				want:=xmlScopeXSD(db,schemaXML,"","",forest,"a","z");if function=="database_to_xmlschema"{want=xmlScopeXSD(db,"","","",forest,schemaXML,emptyXML)};if got!=want{t.Fatalf("scope XSD structure\ngot %s\nwant %s",got,want)}
			};stmt.Close()
		}}}
		for _,forest:=range []bool{false,true}{xmlScopeBoth(t,p,b,c,xmlScopeCall("schema_to_xmlschema","xs_xsd_empty",true,forest,"urn:scope"),xmlScopeXSD(db,emptyXML,"urn:scope","",forest))}
	})
}
