package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXMLMapRowsValues(t *testing.T) {
	xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xmlMapExec(t,p,c, `CREATE DOMAIN xr_bool AS boolean`, `CREATE DOMAIN xr_array AS integer[]`, `CREATE DOMAIN xr_xml AS xml`, `SET timezone='UTC'`, `SET DateStyle='German, DMY'`)
		for _, tc := range []struct{ query, fields string }{
			{`SELECT 1 AS i, true AS t, false AS f`, "  <i>1</i>\n  <t>true</t>\n  <f>false</f>\n"},
			{`SELECT E'&<>\r\n\t"''' AS s`, "  <s>&amp;&lt;&gt;&#x0d;\n\t\"'</s>\n"},
			{`SELECT decode('00ff','hex') AS b`, "  <b>AP8=</b>\n"},
			{`SELECT DATE '2020-02-03' AS d, TIMESTAMP '2020-02-03 04:05:06.120' AS t, TIMESTAMPTZ '2020-02-03 04:05:06+00' AS z`, "  <d>2020-02-03</d>\n  <t>2020-02-03T04:05:06.12</t>\n  <z>2020-02-03T04:05:06+00:00</z>\n"},
			{`SELECT DATE '0001-01-02 BC' AS d,TIMESTAMP '0001-01-02 03:04:05 BC' AS t,TIMESTAMPTZ '0001-01-02 03:04:05+00 BC' AS z`, "  <d>0001-01-02 BC</d>\n  <t>0001-01-02T03:04:05 BC</t>\n  <z>0001-01-02T03:04:05+00:00 BC</z>\n"},
			{`SELECT true::xr_bool AS b, ARRAY[[1,NULL],[3,4]]::xr_array AS a, '<x/>'::xr_xml AS x`, "  <b>true</b>\n  <a><element>1</element><element>3</element><element>4</element></a>\n  <x><x/></x>\n"},
			{`SELECT ARRAY[]::integer[] AS e, ARRAY[NULL,NULL]::integer[] AS n, '[0:1]={2,3}'::integer[] AS l`, "  <e></e>\n  <n></n>\n  <l><element>2</element><element>3</element></l>\n"},
			{`SELECT XMLPARSE(CONTENT '<?xml version="1.1"?><a/>') AS x`, "  <x><?xml version=\"1.1\"?><a/></x>\n"},
		} {
			xmlBoth(t,p,b,c,xmlSelect(`SELECT query_to_xml(`+xmlMapLiteral(tc.query)+`,false,false,'') AS v`,"XML",xmlMapDocument("table","",xmlMapRow(tc.fields))))
		}
		for _, n := range []int{0,1,54,55,108,109} {
			encoded := strings.Repeat("/", n/3*4)
			if n%3==1 { encoded += "/w==" } else if n%3==2 { encoded += "//8=" }
			var chunks []string
			for len(encoded)>72 { chunks=append(chunks,encoded[:72]); encoded=encoded[72:] }
			chunks=append(chunks,encoded)
			q:=fmt.Sprintf(`SELECT query_to_xml('SELECT decode(repeat(''ff'',%d),''hex'') AS b',false,true,'') AS v`,n)
			xmlBoth(t,p,b,c,xmlSelect(q,"XML",xmlMapForest("row","","  <b>"+strings.Join(chunks,"\r\n")+"</b>\n")))
		}
		for _, typ := range []string{"date","timestamp","timestamptz"} {
			kind:="timestamp"; if typ=="date" { kind="date" }
			xmlFailure(t,p,b,c,`SELECT query_to_xml('SELECT ''infinity''::`+typ+` AS x',false,false,'')`,xmlError("22008",kind+" out of range","XML does not support infinite "+kind+" values.",0),false)
		}
		// Held recipes must borrow invocation settings rather than retain Prepare's.
		for _, tc := range []struct{q,setting string; values,wants []string}{
			{`SELECT query_to_xml('SELECT decode(''00ff'',''hex'') AS x',false,true,'') AS v`,"xmlbinary",[]string{"base64","hex","base64"},[]string{"AP8=","00FF","AP8="}},
			{`SELECT query_to_xml('SELECT TIMESTAMPTZ ''2020-02-03 04:05:06+00'' AS x',false,true,'') AS v`,"timezone",[]string{"UTC","Asia/Kolkata","UTC"},[]string{"2020-02-03T04:05:06+00:00","2020-02-03T09:35:06+05:30","2020-02-03T04:05:06+00:00"}},
		} {
			stmt,err:=c.PrepareContext(p.Ctx,tc.q); if err!=nil {t.Fatal(err)}
			for i,v:=range tc.values { xmlMapExec(t,p,c,"SET "+tc.setting+"="+xmlMapLiteral(v)); fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:stmt,SQL:tc.q},b.Recorder,xmlSelect(tc.q,"XML",xmlMapForest("row","","  <x>"+tc.wants[i]+"</x>\n"))) }
			stmt.Close()
		}
	})
	t.Run("typed_xml_notices",xmlMapRowsNotices)
}

func TestReferenceXMLMapRowsNulls(t *testing.T) {
	xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		for _,tc:=range []struct{flag,fields string}{
			{"false","  <a>1</a>\n  <e></e>\n"},
			{"true","  <a>1</a>\n  <b xsi:nil=\"true\"/>\n  <e></e>\n"},
		}{xmlBoth(t,p,b,c,xmlSelect(`SELECT query_to_xml('SELECT 1 AS a,NULL::text AS b,XMLPARSE(CONTENT '''') AS e',`+tc.flag+`,false,'') AS v`,"XML",xmlMapDocument("table","",xmlMapRow(tc.fields))))}
		xmlBoth(t,p,b,c,xmlSelect(`SELECT query_to_xml('SELECT NULL::int AS x',false,true,'') AS v`,"XML",xmlMapForest("row","","")))
	})
}

func TestReferenceXMLMapRowsShapes(t *testing.T) {
	xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		for _,forest:=range []bool{false,true}{
			for _,tc:=range []struct{query,fields string; count int}{
				{`SELECT 1 AS a WHERE false`,"",0},
				{`SELECT FROM generate_series(1,2)`,"",2},
				{`SELECT 1 AS x,2 AS x`,"  <x>1</x>\n  <x>2</x>\n",1},
			}{rows:="";for range tc.count {if forest{rows+=xmlMapForest("row","urn:test",tc.fields)}else{rows+=xmlMapRow(tc.fields)}};want:=rows;if !forest{want=xmlMapDocument("table","urn:test",rows)};xmlBoth(t,p,b,c,xmlSelect(fmt.Sprintf(`SELECT query_to_xml(%s,false,%t,'urn:test') AS v`,xmlMapLiteral(tc.query),forest),"XML",want))}
		}
		xmlBoth(t,p,b,c,xmlSelect(`SELECT query_to_xml('SELECT 1 WHERE false',false,true,'') IS NULL AS v`,"BOOL",false))
		// Name mapping uses the released legacy Name predicates, not XML 1.0 fifth edition.
		q:=`SELECT 1 AS "a:b",2 AS "xmlName",3 AS "XMLName",4 AS "_x0041_",5 AS "😀",6 AS "‌",7 AS "a.b",8 AS "́a",9 AS "á"`
		fields:="  <a_x003A_b>1</a_x003A_b>\n  <_x0078_mlName>2</_x0078_mlName>\n  <_x0058_MLName>3</_x0058_MLName>\n  <_x005F_x0041_>4</_x005F_x0041_>\n  <_x1F600_>5</_x1F600_>\n  <_x200C_>6</_x200C_>\n  <a.b>7</a.b>\n  <_x0301_a>8</_x0301_a>\n  <á>9</á>\n"
		xmlBoth(t,p,b,c,xmlSelect(`SELECT query_to_xml(`+xmlMapLiteral(q)+`,false,true,'') AS v`,"XML",xmlMapForest("row","",fields)))
		xmlMapExec(t,p,c,`CREATE TABLE xr_dropped(a int,gone text,b int)`,`ALTER TABLE xr_dropped DROP COLUMN gone`,`INSERT INTO xr_dropped VALUES(1,2)`)
		xmlBoth(t,p,b,c,xmlSelect(`SELECT table_to_xml('xr_dropped',false,true,'') AS v`,"XML",xmlMapForest("xr_dropped","","  <a>1</a>\n  <b>2</b>\n")))
	})
}

func TestReferenceXMLMapRowsNamespaces(t *testing.T) {
	xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		for _,ns:=range []string{"","urn:test",`a"b`,`a&b`,"a\x01b"}{
			q:=`query_to_xml('SELECT 1 AS a',false,false,`+xmlMapLiteral(ns)+`)`
			want:=xmlMapDocument("table",ns,xmlMapRow("  <a>1</a>\n"))
			for _,expr:=range []string{q,q+`::text`,`XMLSERIALIZE(CONTENT `+q+` AS text)`}{typ:="TEXT";if expr==q{typ="XML"};xmlBoth(t,p,b,c,xmlSelect(`SELECT `+expr+` AS v`,typ,want))}
			if ns==`a"b`||ns==`a&b`||ns=="a\x01b"{for _,suffix:=range []string{` AS text)`,` AS text INDENT)`}{mode:="DOCUMENT";if strings.Contains(suffix,"INDENT"){mode="CONTENT"};xmlFailure(t,p,b,c,`SELECT XMLSERIALIZE(`+mode+` `+q+suffix,xmlError("2200L","not an XML document","",0),false)}}
		}
		q:=`query_to_xml('SELECT chr(1) AS x',false,false,'')`
		want:=xmlMapDocument("table","",xmlMapRow("  <x>\x01</x>\n"))
		for _,expr:=range []string{q,q+`::text`,`XMLSERIALIZE(CONTENT `+q+` AS text)`}{typ:="TEXT";if expr==q{typ="XML"};xmlBoth(t,p,b,c,xmlSelect(`SELECT `+expr+` AS v`,typ,want))}
		for _,expr:=range []string{`XMLSERIALIZE(DOCUMENT `+q+` AS text)`,`XMLSERIALIZE(CONTENT `+q+` AS text INDENT)`}{xmlFailure(t,p,b,c,`SELECT `+expr,xmlError("2200L","not an XML document","",0),false)}
		xmlFailure(t,p,b,c,`SELECT XMLPARSE(CONTENT `+q+`::text)`,xmlError("2200N","invalid XML content","line 4: PCDATA invalid Char value 1\n  <x>\x01</x>\n     ^",0),false)
		xmlFailure(t,p,b,c,`SELECT XMLPARSE(CONTENT query_to_xml('SELECT 1 AS a',false,false,'a&b')::text)`,xmlError("2200N","invalid XML content","line 1: EntityRef: expecting ';'\n<table xmlns:xsi=\"http://www.w3.org/2001/XMLSchema-instance\" xmlns=\"a&b\">\n"+strings.Repeat(" ",71)+"^",0),false)
	})
}
