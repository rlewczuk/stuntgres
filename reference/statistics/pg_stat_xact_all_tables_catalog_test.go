package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatXactAllTablesIdentityAndMetadata(t *testing.T) {
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		tableStatCreate(t,p,b,c,"xat_identity")
		r:=xactTableGet(t,p,c,"xat_identity");xactTableCounts(t,r,0,0,0)
		if r.ID<=0||r.Schema!="public"||r.Name!="xat_identity"||r.Seq!=0||r.Read!=0||r.Index.Valid||r.Fetch.Valid||r.HOT!=0||r.Newpage!=0{t.Fatal("fresh typed row",r)}
		var id,namespace,rowType,array,element,owner int64;var kind string
		err:=c.QueryRowContext(p.Ctx,"SELECT c.oid,c.relnamespace,c.relkind,c.reltype,t.typarray,a.typelem,t.typrelid FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_stat_xact_all_tables'::regclass").Scan(&id,&namespace,&kind,&rowType,&array,&element,&owner)
		if err!=nil||id<=0||namespace!=11||kind!="v"||rowType<=0||array<=0||element!=rowType||owner!=id{t.Fatal("catalog row/array relationships",id,namespace,kind,rowType,array,element,owner,err)}
		if !b.PG&&(id!=12151||rowType!=12153||array!=12152){t.Fatal("native inventory",id,rowType,array)}
		var regType int64;if err:=c.QueryRowContext(p.Ctx,"SELECT 'pg_catalog.pg_stat_xact_all_tables'::regtype::oid").Scan(&regType);err!=nil||regType!=rowType{t.Fatal("regtype identity",regType,err)}
		rows,err:=c.QueryContext(p.Ctx,"SELECT attnum,attname,atttypid,atttypmod FROM pg_attribute WHERE attrelid='pg_catalog.pg_stat_xact_all_tables'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum");if err!=nil{t.Fatal(err)}
		n:=0;for rows.Next(){var number,typ,mod int64;var name string;if err:=rows.Scan(&number,&name,&typ,&mod);err!=nil{t.Fatal(err)};want:=int64(20);if n==0{want=26}else if n<3{want=19};if n>=12||number!=int64(n+1)||name!=xactTableNames[n]||typ!=want||mod!=-1{t.Fatal("attribute shape",n,number,name,typ,mod)};n++};if err:=rows.Err();err!=nil{t.Fatal(err)};rows.Close();if n!=12{t.Fatal("attribute count",n)}
		var definition,information string
		if err:=c.QueryRowContext(p.Ctx,"SELECT pg_get_viewdef('pg_catalog.pg_stat_xact_all_tables'::regclass)").Scan(&definition);err!=nil{t.Fatal(err)}
		if err:=c.QueryRowContext(p.Ctx,"SELECT view_definition FROM information_schema.views WHERE table_schema='pg_catalog' AND table_name='pg_stat_xact_all_tables'").Scan(&information);err!=nil{t.Fatal(err)}
		for _,term:=range []string{"sum(pg_stat_get_xact_numscans(i.indexrelid))","pg_stat_get_xact_tuples_fetched(c.oid)","GROUP BY c.oid, n.nspname, c.relname"}{if !strings.Contains(definition,term)||!strings.Contains(information,term){t.Fatal("released grouped display",term,definition,information)}}
		for _,qualified:=range []bool{false,true}{for _,empty:=range []bool{false,true}{for _,prepared:=range []bool{false,true}{
			q:="SELECT * FROM pg_stat_xact_all_tables WHERE relname=$1";if qualified{q=xactTableSelect};if empty{q+=" AND FALSE"}
			var result *sql.Rows;var stmt *sql.Stmt
			if prepared{stmt,err=c.PrepareContext(p.Ctx,q);if err==nil{result,err=stmt.QueryContext(p.Ctx,"xat_identity")}}else{result,err=c.QueryContext(p.Ctx,q,"xat_identity")};if err!=nil{t.Fatal(err)}
			columns,err:=result.ColumnTypes();if err!=nil{t.Fatal(err)};xactTableMetadata(t,columns,b.PG)
			count:=0;for result.Next(){raw:=make([]any,12);dest:=make([]any,12);for i:=range raw{dest[i]=&raw[i]};if err:=result.Scan(dest...);err!=nil{t.Fatal(err)}
				for i,v:=range raw{switch{case i==5||i==6:if v!=nil{t.Fatal("empty index SUM",i,v)};case i<3:if b.PG{if _,ok:=v.([]byte);!ok{t.Fatalf("pq identity %d=%T",i,v)}}else if i==0{if _,ok:=v.(int64);!ok{t.Fatalf("native OID=%T",v)}}else{if _,ok:=v.(string);!ok{t.Fatalf("native NAME=%T",v)}};default:if v!=int64(0){t.Fatal("typed zero counter",i,v)}}};count++}
			if err:=result.Err();err!=nil{t.Fatal(err)};result.Close();xactTableMetadata(t,columns,b.PG);if stmt!=nil{stmt.Close()};want:=1;if empty{want=0};if count!=want{t.Fatal("cursor cardinality",count,want)}
		}}}
	})
}

func TestReferencePGStatXactAllTablesReadOnly(t *testing.T) {
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		tableStatCreate(t,p,b,c,"xat_readonly");tableStatSeed(t,p,b,"xat_readonly")
		fixture.StatisticsExec(t,p,b,c,"SET stats_fetch_consistency=none",0);fixture.StatisticsExec(t,p,b,c,"SET track_counts=off",0)
		before:=xactTableGet(t,p,c,"xat_readonly");maintenance:=tableStatGet(t,p,c,"xat_readonly")
		for _,tc:=range []struct{q,op string}{
			{"INSERT INTO pg_stat_xact_all_tables(relname) VALUES('x')","INSERT"},
			{"INSERT INTO pg_stat_xact_all_tables(relname) SELECT 'x' WHERE FALSE","INSERT"},
			{"UPDATE pg_stat_xact_all_tables SET seq_scan=9 WHERE relname='xat_readonly'","UPDATE"},
			{"UPDATE pg_stat_xact_all_tables SET seq_scan=9 WHERE FALSE","UPDATE"},
			{"DELETE FROM pg_stat_xact_all_tables WHERE relname='xat_readonly'","DELETE"},
			{"DELETE FROM pg_stat_xact_all_tables WHERE FALSE","DELETE"},
			{"TRUNCATE pg_stat_xact_all_tables","TRUNCATE"},
		}{for _,prepared:=range []bool{false,true}{
			want:=xactTableDiagnostic(tc.op);if tc.op=="TRUNCATE"{want=fixture.Diagnostic{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"42809",Message:`"pg_stat_xact_all_tables" is not a table`}}
			step:=fixture.ErrorDiagnostic(tc.q,want);step.Prepared=prepared;step.ErrorStage="execute";if prepared&&tc.op!="TRUNCATE"{step.ErrorStage="prepare"};fixture.RunStep(t,p.Ctx,c,b.Recorder,step)
			if got:=xactTableGet(t,p,c,"xat_readonly");got!=before{t.Fatal("rejected mutation changed local events",got,before)}
		}}
		pool:=p.Native;if b.PG{pool=p.CopyPostgres};copyConn,err:=pool.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer copyConn.Close()
		result,err:=fixture.CopyFrom(p.Ctx,copyConn,b.PG,"COPY pg_catalog.pg_stat_xact_all_tables(relname) FROM STDIN",strings.NewReader("x\n"))
		fixture.AssertDiagnostic(t,err,&fixture.Diagnostic{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"42809",Message:`cannot copy to view "pg_stat_xact_all_tables"`,Hint:"To enable copying to a view, provide an INSTEAD OF INSERT trigger."})
		if result.Count!=0||result.CommandTag!=""{t.Fatal("rejected COPY count/tag",result)}
		if got:=xactTableGet(t,p,c,"xat_readonly");got!=before{t.Fatal("COPY changed local events",got)}
		if got:=tableStatGet(t,p,c,"xat_readonly");!got.equal(maintenance){t.Fatal("read-only failure changed cumulative maintenance",got)}
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT n,payload FROM xat_readonly ORDER BY n",[]string{"n","payload"},[]string{"INT4","TEXT"},[][]any{{int64(1),"a"},{int64(2),nil},{int64(3),"c"}}))
	})
}
