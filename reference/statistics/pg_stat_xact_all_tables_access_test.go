package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatXactAllTablesAccess(t *testing.T) {
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		tableStatCreate(t,p,b,c,"xat_seq");tableStatSeed(t,p,b,"xat_seq");tableStatCreate(t,p,b,c,"xat_empty")
		w,retire:=fixture.StatisticsReportingConn(t,p,b);defer retire();tableStatSeqPrefer(t,p,b,w)
		fixture.StatisticsExec(t,p,b,w,"BEGIN",0)
		stmt,err:=w.PrepareContext(p.Ctx,"SELECT payload FROM xat_seq WHERE n=$1");if err!=nil{t.Fatal(err)};stmt.Close()
		if r:=xactTableGet(t,p,w,"xat_seq");r.Seq!=0||r.Read!=0{t.Fatal("prepare executed source",r)}
		var scans,read int64
		for i,tc:=range []struct{q,column,typ string;rows [][]any;scans,read int64}{
			{"SELECT payload FROM xat_seq WHERE n=2","payload","TEXT",[][]any{{nil}},1,3},
			{"SELECT payload FROM xat_seq WHERE n=99","payload","TEXT",nil,1,3},
			{"SELECT payload FROM xat_seq WHERE FALSE","payload","TEXT",nil,0,0},
			{"SELECT payload FROM xat_seq LIMIT 0","payload","TEXT",nil,0,0},
			{"SELECT count(*) FROM (SELECT payload FROM xat_seq LIMIT 1) s","count","INT8",[][]any{{int64(1)}},1,1},
		}{
			if i<2{tableStatSeqPlan(t,b,indexStatPlan(t,p,w,tc.q))}
			step:=fixture.Select(tc.q,[]string{tc.column},[]string{tc.typ},tc.rows);step.Prepared=i%2==1;fixture.RunStep(t,p.Ctx,w,b.Recorder,step)
			scans+=tc.scans;read+=tc.read;r:=xactTableGet(t,p,w,"xat_seq");if r.Seq!=scans||r.Read!=read||r.Index.Valid||r.Fetch.Valid{t.Fatal("reached source counts before filter",r,scans,read)}
		}
		fixture.RunStep(t,p.Ctx,w,b.Recorder,fixture.Select("SELECT payload FROM xat_empty",[]string{"payload"},[]string{"TEXT"},nil))
		if r:=xactTableGet(t,p,w,"xat_empty");r.Seq!=1||r.Read!=0{t.Fatal("reached empty heap",r)}
		xactTableEnd(t,p,b,w,"ROLLBACK")
		tableStatCreate(t,p,b,c,"xat_indexes");tableStatSeed(t,p,b,"xat_indexes")
		tableStatBuild(t,p,b,"xat_indexes",3,9,"CREATE INDEX xat_hash_n ON xat_indexes USING hash(n)","CREATE INDEX xat_hash_payload ON xat_indexes USING hash(payload)","CREATE INDEX xat_untouched ON xat_indexes(n) WHERE n<0")
		xactTableIndex(t,xactTableGet(t,p,c,"xat_indexes"),0,0)
		indexed,retireIndexed:=fixture.StatisticsReportingConn(t,p,b);defer retireIndexed();indexStatPrefer(t,p,b,indexed)
		// One simple-query message retains an implicit owner until every result
		// is captured. Unlike BEGIN, it keeps native published index roots
		// eligible, and no terminal flush can race the intervening xact reads.
		cases:=[]struct{q,index string;rows [][]any}{
			{"SELECT payload FROM xat_indexes WHERE n=2","xat_hash_n",[][]any{{nil}}},
			{"SELECT payload FROM xat_indexes WHERE payload='a'","xat_hash_payload",[][]any{{"a"}}},
			{"SELECT payload FROM xat_indexes WHERE n=99","xat_hash_n",nil},
		}
		var commands []string
		for _,tc:=range cases{commands=append(commands,"EXPLAIN (COSTS OFF) "+tc.q,tc.q,"SELECT * FROM pg_stat_xact_all_tables WHERE relname='xat_indexes'")}
		batch,err:=indexed.QueryContext(p.Ctx,strings.Join(commands,";"));if err!=nil{t.Fatal(err)};defer batch.Close()
		for i,tc:=range cases{
			if i>0&&!batch.NextResultSet(){t.Fatal("missing batch plan",i,batch.Err())}
			method:=xactTableBatchPlan(t,batch)
			if !indexStatSimple(t,b,method)||!strings.Contains(method,tc.index){t.Fatal("actual independent implicit index method",method)}
			if !batch.NextResultSet(){t.Fatal("missing actual hash result",batch.Err())}
			fixture.BatchReadSet(t,batch,fixture.BatchSet{Columns:[]string{"payload"},Types:[]string{"TEXT"},Rows:tc.rows})
			if !batch.NextResultSet(){t.Fatal("missing pending xact result",batch.Err())}
			r:=xactTableBatchRow(t,batch,b.PG)
			fetch:=int64(i+1);if i==2{fetch=2};xactTableIndex(t,r,int64(i+1),fetch);if r.Seq!=0||r.Read!=0{t.Fatal("index path manufactured sequential work",r)}
		}
		if batch.NextResultSet()||batch.Err()!=nil{t.Fatal("unexpected hash batch tail",batch.Err())};batch.Close()
		xactTableBitmap(t,p,b,c)
		xactTableIndexOnly(t,p,b,c)
	})
}

func xactTableBitmap(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
	t.Helper()
	for _,q:=range []string{"CREATE EXTENSION bloom","CREATE TABLE xat_bitmap(n integer,payload text)","ALTER TABLE xat_bitmap SET(autovacuum_enabled=false)","INSERT INTO xat_bitmap VALUES(2,'a'),(2,'b'),(3,'c'),(NULL,NULL)"}{count:=int64(0);if strings.HasPrefix(q,"INSERT"){count=4};fixture.StatisticsExec(t,p,b,c,q,count)}
	tableStatBuild(t,p,b,"xat_bitmap",1,4,"CREATE INDEX xat_bitmap_idx ON xat_bitmap USING bloom(n) WITH(length=1,col1=1)")
	w,retire:=fixture.StatisticsReportingConn(t,p,b);defer retire();fixture.StatisticsExec(t,p,b,w,"SET enable_seqscan=off",0)
	q:="SELECT payload FROM xat_bitmap WHERE n=2"
	rows,err:=w.QueryContext(p.Ctx,"EXPLAIN (ANALYZE,COSTS OFF,TIMING OFF,SUMMARY OFF) "+q+";SELECT * FROM pg_stat_xact_all_tables WHERE relname='xat_bitmap'");if err!=nil{t.Fatal(err)};defer rows.Close()
	candidates:=int64(2);if !b.PG{candidates=-1}
	var lines []string
	for rows.Next(){var line string;if err:=rows.Scan(&line);err!=nil{t.Fatal(err)};lines=append(lines,line);trim:=strings.TrimSpace(line)
		if b.PG&&strings.HasPrefix(trim,"Rows Removed by Index Recheck:"){n,err:=strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(trim,"Rows Removed by Index Recheck:")),10,64);if err!=nil{t.Fatal(err)};candidates+=n}
		if !b.PG&&strings.HasPrefix(trim,"Logical Scan on ")&&strings.Contains(trim,"xat_bitmap"){_,after,ok:=strings.Cut(trim,"(actual rows=");var actual float64;var loops int;if !ok{t.Fatal("missing candidate observation",line)};if _,err:=fmt.Sscanf(after,"%f loops=%d)",&actual,&loops);err!=nil||loops!=1||actual!=float64(int64(actual)){t.Fatal("candidate count",line,err)};candidates=int64(actual)}
	};if err:=rows.Err();err!=nil{t.Fatal(err)};if candidates<2{t.Fatal("missing independently observed candidates",candidates)}
	method:=strings.Join(lines,"\n")
	if b.PG{if !strings.Contains(method,"Bitmap Heap Scan")||!strings.Contains(method,"Bitmap Index Scan on xat_bitmap_idx"){t.Fatal("actual bitmap method",method)}}else if !strings.Contains(method,"Access Method: Bloom Candidates"){t.Fatal("actual native candidate method",method)}
	if !rows.NextResultSet(){t.Fatal("missing pending bitmap xact result",rows.Err())}
	r:=xactTableBatchRow(t,rows,b.PG);xactTableIndex(t,r,1,candidates);if r.Seq!=0||r.Read!=0{t.Fatal("bitmap counted sequential work",r)}
	if rows.NextResultSet()||rows.Err()!=nil{t.Fatal("unexpected bitmap batch tail",rows.Err())};rows.Close()
	fixture.StatisticsExec(t,p,b,w,"SET track_counts=off",0)
	step:=fixture.Select(q,[]string{"payload"},[]string{"TEXT"},[][]any{{"a"},{"b"}});step.Bag=true;fixture.RunStep(t,p.Ctx,w,b.Recorder,step)
}

func xactTableIndexOnly(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
	t.Helper();tableStatCreate(t,p,b,c,"xat_only");tableStatSeed(t,p,b,"xat_only");tableStatBuild(t,p,b,"xat_only",1,3,"CREATE INDEX xat_only_tree ON xat_only(n)");fixture.StatisticsExec(t,p,b,c,"VACUUM xat_only",0)
	w,retire:=fixture.StatisticsReportingConn(t,p,b);defer retire()
	for _,q:=range []string{"SET enable_seqscan=off","SET enable_bitmapscan=off","SET enable_indexonlyscan=on","BEGIN"}{fixture.StatisticsExec(t,p,b,w,q,0)}
	if !b.PG{tableStatSeqPlan(t,b,indexStatPlan(t,p,w,"SELECT n FROM xat_only WHERE n=2"));fixture.RunStep(t,p.Ctx,w,b.Recorder,fixture.Select("SELECT n FROM xat_only WHERE n=2",[]string{"n"},[]string{"INT4"},[][]any{{int64(2)}}));r:=xactTableGet(t,p,w,"xat_only");if r.Seq!=1||r.Read!=3{t.Fatal("actual native B-tree fallback",r)};xactTableIndex(t,r,0,0)}else{
		rows,err:=w.QueryContext(p.Ctx,"EXPLAIN (ANALYZE,COSTS OFF,TIMING OFF,SUMMARY OFF) SELECT n FROM xat_only WHERE n=2");if err!=nil{t.Fatal(err)};heap:=int64(-1);var lines []string
		for rows.Next(){var line string;if err:=rows.Scan(&line);err!=nil{t.Fatal(err)};lines=append(lines,line);if _,after,ok:=strings.Cut(strings.TrimSpace(line),"Heap Fetches: ");ok{heap,err=strconv.ParseInt(after,10,64);if err!=nil{t.Fatal(err)}}};if err:=rows.Err();err!=nil{t.Fatal(err)};rows.Close();if !strings.Contains(strings.Join(lines,"\n"),"Index Only Scan using xat_only_tree")||heap<0{t.Fatal("independent physical index-only observation",lines)};r:=xactTableGet(t,p,w,"xat_only");xactTableIndex(t,r,1,heap);if r.Seq!=0||r.Read!=0{t.Fatal("index-only sequential leak",r)}
	}
	xactTableEnd(t,p,b,w,"ROLLBACK")
}

func xactTableBatchPlan(t *testing.T,rows *sql.Rows) string {
	t.Helper();var lines []string
	for rows.Next(){var line string;if err:=rows.Scan(&line);err!=nil{t.Fatal(err)};lines=append(lines,line)}
	if err:=rows.Err();err!=nil{t.Fatal(err)}
	return strings.Join(lines,"\n")
}

func xactTableBatchRow(t *testing.T,rows *sql.Rows,pg bool) xactTableRow {
	t.Helper();columns,err:=rows.ColumnTypes();if err!=nil{t.Fatal(err)};xactTableMetadata(t,columns,pg)
	if !rows.Next(){t.Fatal("pending xact row missing",rows.Err())};var r xactTableRow
	if err:=rows.Scan(r.destinations()...);err!=nil{t.Fatal(err)}
	if rows.Next()||rows.Err()!=nil{t.Fatal("pending xact row cardinality",rows.Err())}
	return r
}
