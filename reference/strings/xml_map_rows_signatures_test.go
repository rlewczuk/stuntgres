package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXMLMapRowsSignatures(t *testing.T) {
	xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		xmlMapExec(t,p,c,`CREATE TABLE xr_signature(a int)`,`INSERT INTO xr_signature VALUES(1)`,`BEGIN`,`DECLARE xr_sig SCROLL CURSOR FOR SELECT 1 AS a`)
		defer c.ExecContext(p.Ctx,`ROLLBACK`)
		for _,tc:=range []struct{name,types,names,vol,parallel string;oid int64; args,casts []string}{
			{"table_to_xml","regclass,boolean,boolean,text","tbl,nulls,tableforest,targetns","s","r",2923,[]string{"'xr_signature'","false","false","''"},[]string{"regclass","boolean","boolean","text"}},
			{"query_to_xml","text,boolean,boolean,text","query,nulls,tableforest,targetns","v","u",2924,[]string{"'SELECT 1 AS a'","false","false","''"},[]string{"text","boolean","boolean","text"}},
			{"cursor_to_xml","refcursor,integer,boolean,boolean,text","cursor,count,nulls,tableforest,targetns","v","u",2925,[]string{"'xr_sig'","0","false","false","''"},[]string{"refcursor","integer","boolean","boolean","text"}},
			{"table_to_xmlschema","regclass,boolean,boolean,text","tbl,nulls,tableforest,targetns","s","r",2926,[]string{"'xr_signature'","false","false","''"},[]string{"regclass","boolean","boolean","text"}},
			{"query_to_xmlschema","text,boolean,boolean,text","query,nulls,tableforest,targetns","v","u",2927,[]string{"'SELECT 1 AS a'","false","false","''"},[]string{"text","boolean","boolean","text"}},
			{"cursor_to_xmlschema","refcursor,boolean,boolean,text","cursor,nulls,tableforest,targetns","v","u",2928,[]string{"'xr_sig'","false","false","''"},[]string{"refcursor","boolean","boolean","text"}},
			{"table_to_xml_and_xmlschema","regclass,boolean,boolean,text","tbl,nulls,tableforest,targetns","s","r",2929,[]string{"'xr_signature'","false","false","''"},[]string{"regclass","boolean","boolean","text"}},
			{"query_to_xml_and_xmlschema","text,boolean,boolean,text","query,nulls,tableforest,targetns","v","u",2930,[]string{"'SELECT 1 AS a'","false","false","''"},[]string{"text","boolean","boolean","text"}},
		} {
			catalog:=fmt.Sprintf(`SELECT oid::bigint AS oid,prorettype::integer AS result,proisstrict,provolatile::text AS volatility,proparallel::text AS parallel,procost::integer AS cost,pronargdefaults::integer AS defaults,array_to_string(proargnames,',') AS names FROM pg_proc WHERE oid=%d`,tc.oid)
			xmlBoth(t,p,b,c,fixture.Select(catalog,[]string{"oid","result","proisstrict","volatility","parallel","cost","defaults","names"},[]string{"INT8","INT4","BOOL","TEXT","TEXT","INT4","INT4","TEXT"},[][]any{{tc.oid,int64(142),true,tc.vol,tc.parallel,int64(100),int64(0),tc.names}}))
			oids:=map[string]string{"regclass":"2205","boolean":"16","text":"25","refcursor":"1790","integer":"23"}
			inputs:=make([]string,len(tc.casts));for i,typ:=range tc.casts{inputs[i]=oids[typ]}
			xmlBoth(t,p,b,c,xmlSelect(fmt.Sprintf(`SELECT proargtypes::text AS v FROM pg_proc WHERE oid=%d`,tc.oid),"TEXT",strings.Join(inputs," ")))
			want:=xmlMapDocument("table","",xmlMapRow("  <a>1</a>\n"))
			root,suffix:="table",""
			if strings.HasPrefix(tc.name,"table_"){root="xr_signature";suffix="."+xmlMapDatabase(t,p,c)+".public.xr_signature";want=xmlMapDocument(root,"",xmlMapRow("  <a>1</a>\n"))}
			schema:=xmlMapIntSchema("a",root,suffix,"",false,false)
			if strings.Contains(tc.name,"xmlschema"){want=schema}
			if strings.Contains(tc.name,"and_xmlschema"){want=xmlMapCombined(root,"",schema,xmlMapRow("  <a>1</a>\n"),false)}
			if tc.name=="cursor_to_xml"{want=xmlMapDocument("table","","")}
			labels:=strings.Split(tc.names,",")
			for _,route:=range []string{"positional","named","typed"}{args:=append([]string(nil),tc.args...);if route=="named"{for i:=range args{args[i]=labels[i]+" => "+args[i]};for i,j:=0,len(args)-1;i<j;i,j=i+1,j-1{args[i],args[j]=args[j],args[i]}};if route=="typed"{for i:=range args{args[i]+="::"+tc.casts[i]}};q:=`SELECT pg_catalog.`+tc.name+`(`+strings.Join(args,",")+`) AS v`;s:=xmlSelect(q,"XML",want);s.CheckMetadata=func(t *testing.T,r *sql.Rows){xmlMetadata(t,r,b.PG,false)};xmlBoth(t,p,b,c,s)}
			// Every strict input independently suppresses inner query/cursor/relation demand.
			for i:=range tc.args{args:=append([]string(nil),tc.args...);args[0]="'missing_cursor'";if strings.HasPrefix(tc.name,"query_"){args[0]="'SELECT no_such_column'"};if strings.HasPrefix(tc.name,"table_"){args[0]="999999::oid::regclass"};args[i]="NULL::"+tc.casts[i];xmlBoth(t,p,b,c,xmlSelect(`SELECT `+tc.name+`(`+strings.Join(args,",")+`) AS v`,"XML",nil))}
		}
		xmlMapExec(t,p,c,`ROLLBACK`)
		// Unknown literal coercions take the ordinary fixed-signature input path.
		xmlBoth(t,p,b,c,xmlSelect(`SELECT query_to_xml('SELECT 1 AS a','no','yes','urn:test') AS v`,"XML",xmlMapForest("row","urn:test","  <a>1</a>\n")))
		for _,tc:=range []struct{query,message string}{
			{`SELECT query_to_xml('SELECT 1',false,false)`,`function query_to_xml(unknown, boolean, boolean) does not exist`},
			{`SELECT query_to_xml('SELECT 1',1,false,'')`,`function query_to_xml(unknown, integer, boolean, unknown) does not exist`},
		}{d:=xmlError("42883",tc.message,"",8);d.Hint="No function matches the given name and argument types. You might need to add explicit type casts.";xmlFailure(t,p,b,c,tc.query,d,true)}
		xmlFailure(t,p,b,c,`SELECT query_to_xml('SELECT 1','bad',false,'')`,xmlError("22P02",`invalid input syntax for type boolean: "bad"`,"",32),true)
		xmlBoth(t,p,b,c,xmlSelect(`SELECT count(*)::integer AS v FROM pg_proc WHERE proname='cursor_to_xml_and_xmlschema'`,"INT4",int64(0)))
	})
}
