package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXMLMapRowsSQLDiagnostics(t *testing.T) {
	xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		xmlMapExec(t,p,c,`CREATE TABLE xr_sql(a int)`,`INSERT INTO xr_sql VALUES(1)`)
		for _,fn:=range []string{"query_to_xml","query_to_xmlschema","query_to_xml_and_xmlschema"}{
			for _,tc:=range []struct{sql,code,message string;pos int;context bool}{
				{`SELECT nope`,"42703",`column "nope" does not exist`,8,false},
				{`SELECT 1; SELECT nope`,"42703",`column "nope" does not exist`,18,false},
				{`SELECT 1; SELECT (`,"42601","syntax error at end of input",19,false},
				{`SELECT 1; SHOW application_name`,"0A000","SHOW is not allowed in a non-volatile function",0,true},
				{`SELECT $1`,"42P02","there is no parameter $1",8,false},
				{`SELECT * FROM absent_xml_rows`,"42P01",`relation "absent_xml_rows" does not exist`,15,false},
				{`SELECT 1/0 AS x`,"22012","division by zero",0,true},
				{`SELECT 1/(g-2) AS x FROM generate_series(1,3) g`,"22012","division by zero",0,true},
				{`SELECT * FROM xr_sql FOR UPDATE`,"0A000","SELECT FOR UPDATE is not allowed in a non-volatile function",0,true},
			}{
				if tc.sql==`SELECT 1; SHOW application_name`&&fn!="query_to_xml"{xmlFailure(t,p,b,c,`SELECT `+fn+`(`+xmlMapLiteral(tc.sql)+`,false,false,'')`,xmlError("42P11","cannot open multi-query plan as cursor","",0),false);continue}
				// A schema-only open does not fetch a row-dependent division.
				if fn=="query_to_xmlschema"&&strings.Contains(tc.sql,"g-2"){xmlBoth(t,p,b,c,xmlSelect(`SELECT `+fn+`(`+xmlMapLiteral(tc.sql)+`,false,false,'') AS v`,"XML",xmlMapIntSchema("x","table","","",false,false)));continue}
				xmlFailure(t,p,b,c,`SELECT `+fn+`(`+xmlMapLiteral(tc.sql)+`,false,false,'')`,xmlMapSPI(tc.code,tc.message,tc.sql,tc.pos,tc.context),false)
			}
			for _,sql:=range []string{"","-- comment","BEGIN",`SET application_name='xml_rows'`,`SELECT 1 INTO xr_into`}{
				code,message:="42P11","cannot open multi-query plan as cursor"
				if sql==`SET application_name='xml_rows'`{message="cannot open SET query as cursor"}
				if sql=="BEGIN"{message="cannot open BEGIN query as cursor"}
				if sql==`SELECT 1 INTO xr_into`{message="cannot open SELECT INTO query as cursor"}
				if fn=="query_to_xml"{code,message="22000","invalid query";if strings.Contains(sql,"INTO"){code,message="0A000","SELECT INTO is not allowed in a non-volatile function"};if strings.HasPrefix(sql,"SET "){code,message="0A000","SET is not allowed in a non-volatile function"}}
				d:=xmlError(code,message,"",0);if fn=="query_to_xml"&&code=="0A000"{d.Context=`SQL statement "`+sql+`"`}
				xmlFailure(t,p,b,c,`SELECT `+fn+`(`+xmlMapLiteral(sql)+`,false,false,'')`,d,false)
			}
			for _,sql:=range []string{`INSERT INTO xr_sql VALUES(2)`,`INSERT INTO xr_sql VALUES(2) RETURNING a`}{code,message:="0A000","INSERT is not allowed in a non-volatile function";context:=true;if fn!="query_to_xml"&&!strings.Contains(sql,"RETURNING"){code,message,context="42P11","cannot open INSERT query as cursor",false};xmlFailure(t,p,b,c,`SELECT `+fn+`(`+xmlMapLiteral(sql)+`,false,false,'')`,xmlMapSPI(code,message,sql,0,context),false)}
		}
		xmlBoth(t,p,b,c,xmlSelect(`SELECT query_to_xml('SELECT 1 AS a; SELECT 2 AS b',false,false,'') AS v`,"XML",xmlMapDocument("table","",xmlMapRow("  <b>2</b>\n"))))
		for _,fn:=range []string{"query_to_xmlschema","query_to_xml_and_xmlschema"}{xmlFailure(t,p,b,c,`SELECT `+fn+`('SELECT 1; SELECT 2',false,false,'')`,xmlError("42P11","cannot open multi-query plan as cursor","",0),false)}
		// SPI_prepare wraps raw utilities without binding/executing them, but
		// still analyzes query-bearing statements before cursor-kind/count checks.
		for _,fn:=range []string{"query_to_xmlschema","query_to_xml_and_xmlschema"}{
			for _,sql:=range []string{`BEGIN; SELECT nope`,`SELECT nope; BEGIN`,`SELECT nope INTO xr_into`}{
				xmlFailure(t,p,b,c,`SELECT `+fn+`(`+xmlMapLiteral(sql)+`,false,false,'')`,xmlMapSPI("42703",`column "nope" does not exist`,sql,strings.Index(sql,"nope")+1,false),false)
			}
			xmlFailure(t,p,b,c,`SELECT `+fn+`('BEGIN; SELECT 1',false,false,'')`,xmlError("42P11","cannot open multi-query plan as cursor","",0),false)
		}
		// SPI parses the full text, but analyzes and executes each command in turn.
		xmlMapExec(t,p,c,`CREATE SEQUENCE xr_prepare_seq`)
		sql:=`SELECT nextval('xr_prepare_seq'); SELECT nope`
		position:=strings.Index(sql,"nope")+1
		xmlFailure(t,p,b,c,`SELECT query_to_xml(`+xmlMapLiteral(sql)+`,false,false,'')`,xmlMapSPI("42703",`column "nope" does not exist`,sql,position,false),false)
		// Both direct and prepared attempts advance the nontransactional sequence
		// before the later command's analysis fails (pinned18.6 tests-2).
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select(`SELECT last_value,is_called FROM xr_prepare_seq`,[]string{"last_value","is_called"},[]string{"INT8","BOOL"},[][]any{{int64(2),true}}))
		// Undefined columns are not hidden by final-command admission.
		sql=`SELECT nope; SELECT 2`
		xmlFailure(t,p,b,c,`SELECT query_to_xml(`+xmlMapLiteral(sql)+`,false,false,'')`,xmlMapSPI("42703",`column "nope" does not exist`,sql,8,false),false)
		xmlBoth(t,p,b,c,xmlSelect(`SELECT count(*)::integer AS v FROM xr_sql`,"INT4",int64(1)))
	})
}

func TestReferenceXMLMapRowsRelations(t *testing.T) {
	fixture.SchemaReferenceCleanup(t,"xr_rel")
	xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		xmlMapExec(t,p,c,`CREATE SCHEMA xr_rel`,`CREATE TABLE xr_rel.xr_a(a int)`,`INSERT INTO xr_rel.xr_a VALUES(1)`,`CREATE VIEW xr_rel.xr_v AS SELECT a+1 AS a FROM xr_rel.xr_a`,`CREATE TABLE xr_parent(a int)`,`CREATE TABLE xr_child() INHERITS(xr_parent)`,`INSERT INTO xr_parent VALUES(1)`,`INSERT INTO xr_child VALUES(2)`,`CREATE SEQUENCE xr_sequence`)
		xmlMapExec(t,p,c,`CREATE TABLE xr_kind(a int)`,`CREATE INDEX xr_kind_idx ON xr_kind(a)`,`CREATE TYPE xr_kind_type AS(a int)`)
		for _,tc:=range []struct{relation,root,fields string}{
			{"xr_rel.xr_a","xr_a","  <a>1</a>\n"},
			{"xr_rel.xr_v","xr_v","  <a>2</a>\n"},
			{"xr_sequence","xr_sequence","  <last_value>1</last_value>\n  <log_cnt>0</log_cnt>\n  <is_called>false</is_called>\n"},
		}{xmlBoth(t,p,b,c,xmlSelect(`SELECT table_to_xml(`+xmlMapLiteral(tc.relation)+`,false,false,'') AS v`,"XML",xmlMapDocument(tc.root,"",xmlMapRow(tc.fields))))}
		sequenceTypes:="<xsd:simpleType name=\"BIGINT\">\n  <xsd:restriction base=\"xsd:long\">\n    <xsd:maxInclusive value=\"9223372036854775807\"/>\n    <xsd:minInclusive value=\"-9223372036854775808\"/>\n  </xsd:restriction>\n</xsd:simpleType>\n\n<xsd:simpleType name=\"BOOLEAN\">\n  <xsd:restriction base=\"xsd:boolean\"></xsd:restriction>\n</xsd:simpleType>\n\n"
		sequenceColumns:=xmlMapColumn("last_value","BIGINT",false)+xmlMapColumn("log_cnt","BIGINT",false)+xmlMapColumn("is_called","BOOLEAN",false)
		xmlBoth(t,p,b,c,xmlSelect(`SELECT table_to_xmlschema('xr_sequence',false,false,'') AS v`,"XML",xmlMapSchema(sequenceTypes,sequenceColumns,"xr_sequence","."+xmlMapDatabase(t,p,c)+".public.xr_sequence","",false)))
		// Order is asserted through an ordinary SQL query; inheritance is real SELECT semantics.
		xmlBoth(t,p,b,c,xmlSelect(`SELECT query_to_xml('SELECT a FROM xr_parent ORDER BY a',false,false,'') AS v`,"XML",xmlMapDocument("table","",xmlMapRow("  <a>1</a>\n")+xmlMapRow("  <a>2</a>\n"))))
		xmlBoth(t,p,b,c,xmlSelect(`SELECT table_to_xml('xr_parent',false,false,'') AS v`,"XML",xmlMapDocument("xr_parent","",xmlMapRow("  <a>1</a>\n")+xmlMapRow("  <a>2</a>\n"))))
		for _,fn:=range []string{"table_to_xml","table_to_xmlschema","table_to_xml_and_xmlschema"}{
			for _,tc:=range []struct{name,kind string}{{"xr_kind_idx","indexes"},{"xr_kind_type","composite types"}}{
				d:=xmlError("42809",`cannot open relation "`+tc.name+`"`,"This operation is not supported for "+tc.kind+".",0)
				if fn=="table_to_xml"{d.InternalPosition,d.InternalQuery="15","SELECT * FROM "+tc.name}
				xmlFailure(t,p,b,c,`SELECT `+fn+`(`+xmlMapLiteral(tc.name)+`,false,false,'')`,d,false)
			}
			q:=`SELECT `+fn+`('absent_xml_rows',false,false,'')`
			xmlFailure(t,p,b,c,q,xmlError("42P01",`relation "absent_xml_rows" does not exist`,"",strings.Index(q,"'absent")+1),true)
			q=`SELECT 'é' AS marker, `+fn+`('absent_xml_rows',false,false,'')`
			pos:=len([]rune(q[:strings.Index(q,"'absent")]))+1
			xmlFailure(t,p,b,c,q,xmlError("42P01",`relation "absent_xml_rows" does not exist`,"",pos),true)
			d:=xmlError("XX000","could not open relation with OID 999999","",0)
			if fn=="table_to_xml"{d=xmlMapSPI("42601",`syntax error at or near "999999"`,`SELECT * FROM 999999`,15,false)}
			xmlFailure(t,p,b,c,`SELECT `+fn+`(999999::oid::regclass,false,false,'')`,d,false)
		}
	})
}

func TestReferenceXMLMapRowsPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"xr_reader"})
	fixture.SchemaReferenceCleanup(t,"xr_hidden")
	xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		xmlMapExec(t,p,c,`CREATE ROLE xr_reader`,`GRANT USAGE ON SCHEMA public TO xr_reader`,`CREATE TABLE xr_acl(a int,b int)`,`INSERT INTO xr_acl VALUES(1,2)`,`CREATE SCHEMA xr_hidden`,`CREATE TABLE xr_hidden.t(a int)`,`CREATE VIEW xr_definer AS SELECT a FROM xr_acl`,`CREATE VIEW xr_invoker WITH(security_invoker=true) AS SELECT a FROM xr_acl`,`GRANT SELECT ON xr_definer,xr_invoker TO xr_reader`,`SET ROLE xr_reader`)
		defer c.ExecContext(p.Ctx,`RESET ROLE`)
		for _,fn:=range []string{"table_to_xml","table_to_xml_and_xmlschema"}{xmlFailure(t,p,b,c,`SELECT `+fn+`('xr_acl',false,false,'')`,xmlMapSPI("42501","permission denied for table xr_acl","SELECT * FROM xr_acl",0,true),false)}
		for _,fn:=range []string{"query_to_xml","query_to_xmlschema","query_to_xml_and_xmlschema"}{xmlFailure(t,p,b,c,`SELECT `+fn+`('SELECT * FROM xr_acl',false,false,'')`,xmlMapSPI("42501","permission denied for table xr_acl","SELECT * FROM xr_acl",0,true),false)}
		db:=xmlMapDatabase(t,p,c)
		want:=xmlMapSchema(xmlMapInteger,xmlMapColumn("a","INTEGER",false)+xmlMapColumn("b","INTEGER",false),"xr_acl","."+db+".public.xr_acl","",false)
		xmlBoth(t,p,b,c,xmlSelect(`SELECT table_to_xmlschema('xr_acl',false,false,'') AS v`,"XML",want))
		for _,fn:=range []string{"table_to_xml","table_to_xmlschema","table_to_xml_and_xmlschema"}{q:=`SELECT `+fn+`('xr_hidden.t',false,false,'')`;xmlFailure(t,p,b,c,q,xmlError("42501","permission denied for schema xr_hidden","",strings.Index(q,"'xr_hidden")+1),true)}
		xmlBoth(t,p,b,c,xmlSelect(`SELECT table_to_xml('xr_definer',false,false,'') AS v`,"XML",xmlMapDocument("xr_definer","",xmlMapRow("  <a>1</a>\n"))))
		xmlFailure(t,p,b,c,`SELECT table_to_xml('xr_invoker',false,false,'')`,xmlMapSPI("42501","permission denied for table xr_acl","SELECT * FROM xr_invoker",0,true),false)
		xmlMapExec(t,p,c,`RESET ROLE`,`GRANT SELECT(a) ON xr_acl TO xr_reader`,`SET ROLE xr_reader`)
		xmlBoth(t,p,b,c,xmlSelect(`SELECT query_to_xml('SELECT a FROM xr_acl',false,false,'') AS v`,"XML",xmlMapDocument("table","",xmlMapRow("  <a>1</a>\n"))))
		xmlFailure(t,p,b,c,`SELECT table_to_xml('xr_acl',false,false,'')`,xmlMapSPI("42501","permission denied for table xr_acl","SELECT * FROM xr_acl",0,true),false)
		xmlBoth(t,p,b,c,xmlSelect(`SELECT table_to_xml('xr_invoker',false,false,'') AS v`,"XML",xmlMapDocument("xr_invoker","",xmlMapRow("  <a>1</a>\n"))))
		xmlMapExec(t,p,c,`RESET ROLE`,`GRANT SELECT ON xr_acl TO xr_reader`,`ALTER TABLE xr_acl ENABLE ROW LEVEL SECURITY`,`CREATE POLICY xr_policy ON xr_acl TO xr_reader USING(a=2)`,`SET ROLE xr_reader`)
		xmlBoth(t,p,b,c,xmlSelect(`SELECT table_to_xml('xr_acl',false,false,'') AS v`,"XML",xmlMapDocument("xr_acl","","")))
		xmlMapExec(t,p,c,`RESET ROLE`,`INSERT INTO xr_acl VALUES(2,3)`,`SET ROLE xr_reader`)
		xmlBoth(t,p,b,c,xmlSelect(`SELECT table_to_xml('xr_acl',false,false,'') AS v`,"XML",xmlMapDocument("xr_acl","",xmlMapRow("  <a>2</a>\n  <b>3</b>\n"))))
		xmlMapExec(t,p,c,`RESET ROLE`)
	})
}
