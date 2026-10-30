package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXMLMapRowsSchemas(t *testing.T) {
	xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		for _,nulls:=range []bool{false,true}{for _,forest:=range []bool{false,true}{for _,ns:=range []string{"","urn:test",`a"b`}{root:="table";if forest{root="row"};want:=xmlMapIntSchema("a",root,"",ns,nulls,forest);xmlBoth(t,p,b,c,xmlSelect(fmt.Sprintf(`SELECT query_to_xmlschema('SELECT 1 AS a',%t,%t,%s) AS v`,nulls,forest,xmlMapLiteral(ns)),"XML",want))}}}
		xmlBoth(t,p,b,c,xmlSelect(`SELECT query_to_xmlschema('SELECT FROM generate_series(1,2)',false,false,'') AS v`,"XML",xmlMapSchema("","","table","","",false)))
		xmlBoth(t,p,b,c,xmlSelect(`SELECT query_to_xmlschema('SELECT 1 AS x,2 AS x',false,true,'') AS v`,"XML",xmlMapSchema(xmlMapInteger,xmlMapColumn("x","INTEGER",false)+xmlMapColumn("x","INTEGER",false),"row","","",true)))
		db:=xmlMapDatabase(t,p,c)
		xmlMapExec(t,p,c,`CREATE TABLE "xr.schema"(a integer NOT NULL)`,`INSERT INTO "xr.schema" VALUES(1)`)
		xmlBoth(t,p,b,c,xmlSelect(`SELECT table_to_xmlschema('"xr.schema"',false,false,'') AS v`,"XML",xmlMapIntSchema("a","xr.schema","."+db+".public.xr_x002E_schema","",false,false)))
		xmlBoth(t,p,b,c,xmlSelect(`SELECT table_to_xmlschema('"xr.schema"',true,false,'') AS v`,"XML",xmlMapIntSchema("a","xr.schema","."+db+".public.xr_x002E_schema","",true,false)))
		// Each restriction is independently transcribed from released xml.c:3822-3988.
		for _,tc:=range []struct{expr,name,definition string}{
			{"1::smallint","SMALLINT","<xsd:simpleType name=\"SMALLINT\">\n  <xsd:restriction base=\"xsd:short\">\n    <xsd:maxInclusive value=\"32767\"/>\n    <xsd:minInclusive value=\"-32768\"/>\n  </xsd:restriction>\n</xsd:simpleType>\n\n"},
			{"1::bigint","BIGINT","<xsd:simpleType name=\"BIGINT\">\n  <xsd:restriction base=\"xsd:long\">\n    <xsd:maxInclusive value=\"9223372036854775807\"/>\n    <xsd:minInclusive value=\"-9223372036854775808\"/>\n  </xsd:restriction>\n</xsd:simpleType>\n\n"},
			{"true","BOOLEAN","<xsd:simpleType name=\"BOOLEAN\">\n  <xsd:restriction base=\"xsd:boolean\"></xsd:restriction>\n</xsd:simpleType>\n\n"},
			{"1::real","REAL","<xsd:simpleType name=\"REAL\">\n  <xsd:restriction base=\"xsd:float\"></xsd:restriction>\n</xsd:simpleType>\n\n"},
			{"1::double precision","DOUBLE","<xsd:simpleType name=\"DOUBLE\">\n  <xsd:restriction base=\"xsd:double\"></xsd:restriction>\n</xsd:simpleType>\n\n"},
			{"'x'::text","UDT."+db+".pg_catalog.text","<xsd:simpleType name=\"UDT."+db+".pg_catalog.text\">\n  <xsd:restriction base=\"xsd:string\">\n  </xsd:restriction>\n</xsd:simpleType>\n\n"},
			{"'x'::varchar(3)","VARCHAR","<xsd:simpleType name=\"VARCHAR\">\n  <xsd:restriction base=\"xsd:string\">\n  </xsd:restriction>\n</xsd:simpleType>\n\n"},
			{"'x'::char(3)","CHAR","<xsd:simpleType name=\"CHAR\">\n  <xsd:restriction base=\"xsd:string\">\n  </xsd:restriction>\n</xsd:simpleType>\n\n"},
			{"1::numeric(5,2)","NUMERIC","<xsd:simpleType name=\"NUMERIC\">\n</xsd:simpleType>\n\n"},
			{"ARRAY[1]","UDT."+db+".pg_catalog._int4","<xsd:simpleType name=\"UDT."+db+".pg_catalog._int4\">\n</xsd:simpleType>\n\n"},
			{"'<a/>'::xml","XML","<xsd:complexType mixed=\"true\">\n  <xsd:sequence>\n    <xsd:any name=\"element\" minOccurs=\"0\" maxOccurs=\"unbounded\" processContents=\"skip\"/>\n  </xsd:sequence>\n</xsd:complexType>\n\n"},
			{"DATE '2020-01-02'","DATE","<xsd:simpleType name=\"DATE\">\n  <xsd:restriction base=\"xsd:date\">\n    <xsd:pattern value=\"\\p{Nd}{4}-\\p{Nd}{2}-\\p{Nd}{2}\"/>\n  </xsd:restriction>\n</xsd:simpleType>\n\n"},
		}{want:=xmlMapSchema(tc.definition,xmlMapColumn("a",tc.name,false),"table","","",false);xmlBoth(t,p,b,c,xmlSelect(`SELECT query_to_xmlschema(`+xmlMapLiteral("SELECT "+tc.expr+" AS a")+`,false,false,'') AS v`,"XML",want))}
		for _,tc:=range []struct{typ,name,base,pattern string}{
			{"time","TIME","time",`\p{Nd}{2}:\p{Nd}{2}:\p{Nd}{2}(.\p{Nd}+)?`},
			{"timetz","TIME_WTZ","time",`\p{Nd}{2}:\p{Nd}{2}:\p{Nd}{2}(.\p{Nd}+)?(\+|-)\p{Nd}{2}:\p{Nd}{2}`},
			{"timestamp","TIMESTAMP","dateTime",`\p{Nd}{4}-\p{Nd}{2}-\p{Nd}{2}T\p{Nd}{2}:\p{Nd}{2}:\p{Nd}{2}(.\p{Nd}+)?`},
			{"timestamptz","TIMESTAMP_WTZ","dateTime",`\p{Nd}{4}-\p{Nd}{2}-\p{Nd}{2}T\p{Nd}{2}:\p{Nd}{2}:\p{Nd}{2}(.\p{Nd}+)?(\+|-)\p{Nd}{2}:\p{Nd}{2}`},
		}{definition:=`<xsd:simpleType name="`+tc.name+`">`+"\n  <xsd:restriction base=\"xsd:"+tc.base+"\">\n    <xsd:pattern value=\""+tc.pattern+"\"/>\n  </xsd:restriction>\n</xsd:simpleType>\n\n";xmlBoth(t,p,b,c,xmlSelect(`SELECT query_to_xmlschema('SELECT NULL::`+tc.typ+` AS a',false,false,'') AS v`,"XML",xmlMapSchema(definition,xmlMapColumn("a",tc.name,false),"table","","",false)))}
		for _,mode:=range []string{"base64","hex"}{xmlMapExec(t,p,c,"SET xmlbinary="+xmlMapLiteral(mode));base:="base64Binary";if mode=="hex"{base="hexBinary"};definition:="<xsd:simpleType name=\"UDT."+db+".pg_catalog.bytea\">\n  <xsd:restriction base=\"xsd:"+base+"\">\n  </xsd:restriction>\n</xsd:simpleType>\n\n";xmlBoth(t,p,b,c,xmlSelect(`SELECT query_to_xmlschema('SELECT NULL::bytea AS a',false,false,'') AS v`,"XML",xmlMapSchema(definition,xmlMapColumn("a","UDT."+db+".pg_catalog.bytea",false),"table","","",false)))}
		xmlMapExec(t,p,c,`CREATE DOMAIN xr_domain AS integer`,`CREATE DOMAIN xr_nested AS xr_domain`)
		domain:="Domain."+db+".public.xr_nested"
		types:=`<xsd:simpleType name="`+domain+`">`+"\n  <xsd:restriction base=\"INTEGER\"/>\n</xsd:simpleType>\n\n"+xmlMapInteger
		xmlBoth(t,p,b,c,xmlSelect(`SELECT query_to_xmlschema('SELECT 1::xr_nested AS d,2 AS i,3::xr_nested AS e',false,false,'') AS v`,"XML",xmlMapSchema(types,xmlMapColumn("d",domain,false)+xmlMapColumn("i","INTEGER",false)+xmlMapColumn("e",domain,false),"table","","",false)))
		xmlBoth(t,p,b,c,xmlSelect(`SELECT query_to_xmlschema('SELECT 1::xr_nested AS d',false,false,'') AS v`,"XML",xmlMapSchema(types,xmlMapColumn("d",domain,false),"table","","",false)))
		xmlMapExec(t,p,c,`CREATE DOMAIN xr_varchar AS varchar(3)`)
		varcharDomain:="Domain."+db+".public.xr_varchar"
		varcharTypes:=`<xsd:simpleType name="`+varcharDomain+`">`+"\n  <xsd:restriction base=\"VARCHAR_3\"/>\n</xsd:simpleType>\n\n<xsd:simpleType name=\"VARCHAR\">\n  <xsd:restriction base=\"xsd:string\">\n  </xsd:restriction>\n</xsd:simpleType>\n\n"
		xmlBoth(t,p,b,c,xmlSelect(`SELECT query_to_xmlschema('SELECT NULL::xr_varchar AS d',false,false,'') AS v`,"XML",xmlMapSchema(varcharTypes,xmlMapColumn("d",varcharDomain,false),"table","","",false)))
		// Descriptor discovery opens but never FETCHes volatile row expressions.
		xmlMapExec(t,p,c,`CREATE SEQUENCE xr_schema_seq`)
		xmlBoth(t,p,b,c,xmlSelect(`SELECT query_to_xmlschema('SELECT nextval(''xr_schema_seq'')::integer AS a',false,false,'') AS v`,"XML",xmlMapIntSchema("a","table","","",false,false)))
		xmlBoth(t,p,b,c,fixture.Select(`SELECT last_value,is_called FROM xr_schema_seq`,[]string{"last_value","is_called"},[]string{"INT8","BOOL"},[][]any{{int64(1),false}}))
	})
}

func TestReferenceXMLMapRowsCombined(t *testing.T) {
	xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		xmlMapExec(t,p,c,`CREATE TABLE xr_combined(a int)`,`INSERT INTO xr_combined VALUES(1)`)
		for _,relation:=range []bool{false,true}{for _,forest:=range []bool{false,true}{for _,ns:=range []string{"","urn:test",`a&b`}{
			fn,arg,root,suffix:="query_to_xml_and_xmlschema","SELECT 1 AS a","table",""
			if relation{fn,arg,root,suffix="table_to_xml_and_xmlschema","xr_combined","xr_combined","."+xmlMapDatabase(t,p,c)+".public.xr_combined"}
			schemaRoot:=root;if forest&&!relation{schemaRoot="row"}
			schema:=xmlMapIntSchema("a",schemaRoot,suffix,ns,false,forest);rows:=xmlMapRow("  <a>1</a>\n");if forest{rows=xmlMapForest(schemaRoot,ns,"  <a>1</a>\n")}
			xmlBoth(t,p,b,c,xmlSelect(fmt.Sprintf(`SELECT %s(%s,false,%t,%s) AS v`,fn,xmlMapLiteral(arg),forest,xmlMapLiteral(ns)),"XML",xmlMapCombined(root,ns,schema,rows,forest)))
		}}}
		for _,nulls:=range []bool{false,true}{fields:="";if nulls{fields="  <a xsi:nil=\"true\"/>\n"};schema:=xmlMapIntSchema("a","table","","",nulls,false);xmlBoth(t,p,b,c,xmlSelect(fmt.Sprintf(`SELECT query_to_xml_and_xmlschema('SELECT NULL::int AS a',%t,false,'') AS v`,nulls),"XML",xmlMapCombined("table","",schema,xmlMapRow(fields),false)))}
		xmlMapExec(t,p,c,`CREATE SEQUENCE xr_combined_seq`)
		q:=`SELECT query_to_xml_and_xmlschema('SELECT nextval(''xr_combined_seq'')::int AS a',false,false,'') AS v`
		// Once per execution, not once in schema discovery plus once in row demand.
		for _,prepared:=range []bool{false,true}{want:=1;if prepared{want=2};step:=xmlSelect(q,"XML",xmlMapCombined("table","",xmlMapIntSchema("a","table","","",false,false),xmlMapRow(fmt.Sprintf("  <a>%d</a>\n",want)),false));if prepared{step=fixture.Prepared(step)};fixture.RunStep(t,p.Ctx,c,b.Recorder,step)}
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select(`SELECT last_value,is_called FROM xr_combined_seq`,[]string{"last_value","is_called"},[]string{"INT8","BOOL"},[][]any{{int64(2),true}}))
	})
}
