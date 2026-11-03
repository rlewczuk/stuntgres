package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferencePGStatAllTablesSnapshotsAndLifetime(t *testing.T){
	fixture.SchemaReferenceCleanup(t,"sat_cache_a");fixture.SchemaReferenceCleanup(t,"sat_cache_b");fixture.SchemaReferenceCleanup(t,"sat_moved")
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		for _,q:=range []string{"CREATE SCHEMA sat_cache_a","CREATE SCHEMA sat_cache_b","CREATE SCHEMA sat_moved","SET stats_fetch_consistency=none"}{fixture.StatisticsExec(t,p,b,c,q,0)}
		for _,mode:=range []string{"none","cache","snapshot"}{for _,key:=range []string{"relid","schemaname","relname"}{t.Run(mode+"/"+key,func(t *testing.T){
			nameA,nameB:="sat_"+mode+"_"+key+"_a","sat_"+mode+"_"+key+"_b"
			a,bname:="sat_cache_a."+nameA,"sat_cache_b."+nameB
			for _,name:=range []string{a,bname}{tableStatCreate(t,p,b,c,name);w,retire:=fixture.StatisticsReportingConn(t,p,b);fixture.StatisticsExec(t,p,b,w,"INSERT INTO "+name+" VALUES(1,'a'),(2,NULL),(3,'c')",3);retire()}
			for _,name:=range []string{nameA,nameB}{tableStatAwait(t,p,b,name,"seed3",func(r tableStatRow)bool{return r.Inserts==3})}
			tableStatBuild(t,p,b,nameA,1,3,"CREATE INDEX "+nameA+"_idx ON "+a+" USING hash(n)")
			tableStatBuild(t,p,b,nameB,1,3,"CREATE INDEX "+nameB+"_idx ON "+bname+" USING hash(n)")
			reader:=fixture.IdentityConnection(t,p.Ctx,b.DB)
			fixture.StatisticsExec(t,p,b,reader,"SET stats_fetch_consistency="+mode,0);fixture.StatisticsExec(t,p,b,reader,"BEGIN",0);defer reader.ExecContext(context.Background(),"ROLLBACK")
			predicate,arg:="relname=$1",any(nameA);if key=="schemaname"{predicate,arg="schemaname=$1","sat_cache_a"};if key=="relid"{predicate,arg="relid=$1",tableStatGet(t,p,c,nameA).ID}
			// Restrict further only for the schema case's existing objects, without
			// reading any B table or index fact into this transaction's cache.
			q:="SELECT * FROM pg_stat_all_tables WHERE "+predicate
			rows,err:=reader.QueryContext(p.Ctx,q,arg);if err!=nil{t.Fatal(err)};found:=false;for rows.Next(){var r tableStatRow;if err:=rows.Scan(r.destinations()...);err!=nil{t.Fatal(err)};if r.Name==nameA{found=true;if r.Seq!=1||r.Read!=3||!r.Index.Valid||r.Index.Int64!=0{t.Fatal("initial post-build reader",r)}}};if err:=rows.Err();err!=nil{t.Fatal(err)};rows.Close();if !found{t.Fatal("A row not captured")}
			w,retire:=fixture.StatisticsReportingConn(t,p,b)
			for _,name:=range []string{a,bname}{tableStatSeqPrefer(t,p,b,w);fixture.RunStep(t,p.Ctx,w,b.Recorder,fixture.Select("SELECT payload FROM "+name+" WHERE n=2",[]string{"payload"},[]string{"TEXT"},[][]any{{nil}}));fixture.StatisticsExec(t,p,b,w,"SET enable_indexscan=on",0);indexStatPrefer(t,p,b,w);if !indexStatSimple(t,b,indexStatPlan(t,p,w,"SELECT payload FROM "+name+" WHERE n=2")){t.Fatal("cache writer not indexed")};fixture.RunStep(t,p.Ctx,w,b.Recorder,fixture.Select("SELECT payload FROM "+name+" WHERE n=2",[]string{"payload"},[]string{"TEXT"},[][]any{{nil}}))};retire()
			for _,name:=range []string{nameA,nameB}{tableStatAwait(t,p,b,name,"heap+index publication",func(r tableStatRow)bool{return r.Seq==2&&r.Index.Valid&&r.Index.Int64==1})}
			wantA,wantB:=int64(0),int64(1);if mode=="none"{wantA=1};if mode=="snapshot"{wantB=0}
			for _,tc:=range []struct{name string;want int64}{{nameA,wantA},{nameB,wantB}}{r:=tableStatGet(t,p,reader,tc.name);if r.Seq!=1+tc.want||r.Read!=3+3*tc.want||!r.Index.Valid||r.Index.Int64!=tc.want||!r.Fetch.Valid||r.Fetch.Int64!=tc.want{t.Fatal("predicate-before-reader cache semantics",mode,key,tc,r)}}
			fixture.StatisticsClearSnapshot(t,p,b,reader)
			for _,name:=range []string{nameA,nameB}{r:=tableStatGet(t,p,reader,name);if r.Seq!=2||r.Read!=6{t.Fatal("clear reader cut",r)};tableStatIndexWant(t,r,1,1)}
			fixture.StatisticsExec(t,p,b,reader,"COMMIT",0)
		})}}
		tableStatCreate(t,p,b,c,"sat_lifetime");tableStatSeed(t,p,b,"sat_lifetime")
		old:=tableStatGet(t,p,c,"sat_lifetime")
		w,retire:=fixture.StatisticsReportingConn(t,p,b)
		for _,q:=range []string{"SET track_counts=off","SET track_activities=off","SET track_functions=none","SET track_io_timing=off"}{fixture.StatisticsExec(t,p,b,w,q,0)}
		fixture.RunStep(t,p.Ctx,w,b.Recorder,fixture.Select("SELECT payload FROM sat_lifetime WHERE n=2",[]string{"payload"},[]string{"TEXT"},[][]any{{nil}}))
		fixture.StatisticsExec(t,p,b,w,"ANALYZE sat_lifetime",0)
		fixture.StatisticsExec(t,p,b,w,"SET track_counts=on",0)
		fixture.RunStep(t,p.Ctx,w,b.Recorder,fixture.Select("SELECT payload FROM sat_lifetime WHERE n=2",[]string{"payload"},[]string{"TEXT"},[][]any{{nil}}));retire()
		positive:=tableStatAwait(t,p,b,"sat_lifetime","future-only counts enable",func(r tableStatRow)bool{return r.Seq==1})
		if positive.Read!=3||positive.Analyze!=0{t.Fatal("counts policy or unrelated settings gate",positive)}
		fixture.StatisticsExec(t,p,b,c,"BEGIN",0);fixture.StatisticsExec(t,p,b,c,"DROP TABLE sat_lifetime",0);fixture.StatisticsExec(t,p,b,c,"ROLLBACK",0)
		if got:=tableStatGet(t,p,c,"sat_lifetime");!got.equal(positive){t.Fatal("rolled-back drop changed history",got)}
		heldConn:=fixture.IdentityConnection(t,p.Ctx,b.DB)
		held,err:=heldConn.QueryContext(p.Ctx,tableStatSelect,"sat_lifetime")
		if err!=nil {t.Fatal(err)}
		defer held.Close()
		fixture.StatisticsExec(t,p,b,c,"ALTER TABLE sat_lifetime RENAME TO sat_renamed",0);fixture.StatisticsExec(t,p,b,c,"ALTER TABLE sat_renamed SET SCHEMA sat_moved",0)
		moved:=tableStatGet(t,p,c,"sat_renamed");if moved.ID!=positive.ID||moved.Schema!="sat_moved"||moved.Seq!=1||moved.Inserts!=3{t.Fatal("qualified identity reminted",moved)}
		fixture.StatisticsExec(t,p,b,c,"DROP TABLE sat_moved.sat_renamed",0)
		if !held.Next() {t.Fatal("retained named row missing",held.Err())}
		var retainedNamed tableStatRow
		if err:=held.Scan(retainedNamed.destinations()...);err!=nil {t.Fatal(err)}
		held.Close()
		if !retainedNamed.equal(positive) {t.Fatal("retained result changed through rename/move/drop",retainedNamed,positive)}
		if _,err:=tableStatRead(p.Ctx,c,"sat_renamed");err!=sql.ErrNoRows{t.Fatal("dropped descriptor resurrected",err)}
		tableStatCreate(t,p,b,c,"sat_lifetime");fresh:=tableStatGet(t,p,c,"sat_lifetime");if fresh.ID==old.ID||fresh.Inserts!=0||fresh.Seq!=0||fresh.LastSeq.Valid{t.Fatal("same-name incarnation inherited history",fresh,old)}
		if old.Seq!=0||old.Inserts!=3||positive.Seq!=1||positive.Schema!="public"{t.Fatal("retained rows mutated",old,positive)}
	})
}

func TestReferencePGStatAllTablesNativeControls(t *testing.T){
	var emitted []byte
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		if b.PG{
			if len(emitted)==0{t.Fatal("missing actual native dump")};if _,err:=c.ExecContext(p.Ctx,string(emitted));err!=nil{t.Fatal("real PostgreSQL dump replay",err)}
			fixture.StatisticsExec(t,p,b,c,"SET stats_fetch_consistency=none",0)
			initial:=tableStatGet(t,p,c,"sat_control_rows");if initial.Seq!=0||initial.LastSeq.Valid{t.Fatal("dump serialized runtime history",initial)}
			w,retire:=fixture.StatisticsReportingConn(t,p,b);tableStatSeqPrefer(t,p,b,w);fixture.RunStep(t,p.Ctx,w,b.Recorder,fixture.Select("SELECT n,payload FROM sat_control_rows ORDER BY n",[]string{"n","payload"},[]string{"INT4","TEXT"},[][]any{{int64(1),"a"},{int64(2),nil},{int64(3),"c"}}));retire()
			tableStatAwait(t,p,b,"sat_control_rows","fresh oracle execution",func(r tableStatRow)bool{return r.Seq==1&&r.Read==3});return
		}
		driver:=stuntgres.NewDriver();connector,err:=driver.OpenConnector("table-stat-source");if err!=nil{t.Fatal(err)};source:=sql.OpenDB(connector);defer source.Close()
		recorded,recorder:=fixture.RecordedNativePool(t,connector);sourceBackend:=b;sourceBackend.DB,sourceBackend.Recorder=recorded,recorder
		sourceConn,err:=recorded.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer sourceConn.Close()
		tableStatCreate(t,p,sourceBackend,sourceConn,"sat_control_rows");tableStatSeed(t,p,sourceBackend,"sat_control_rows")
		sourceWriter,retireSource:=fixture.StatisticsReportingConn(t,p,sourceBackend);fixture.RunStep(t,p.Ctx,sourceWriter,recorder,fixture.Select("SELECT payload FROM sat_control_rows WHERE n=2",[]string{"payload"},[]string{"TEXT"},[][]any{{nil}}));retireSource()
		tableStatAwait(t,p,sourceBackend,"sat_control_rows","source positive history",func(r tableStatRow)bool{return r.Seq==1&&r.Read==3})
		fixture.StatisticsExec(t,p,sourceBackend,sourceConn,"CREATE TEMP TABLE sat_source_temp(n integer)",0)
		var dump bytes.Buffer;if err:=stuntgres.Dump(p.Ctx,source,&dump);err!=nil{t.Fatal(err)};emitted=bytes.Clone(dump.Bytes())
		if bytes.Contains(emitted,[]byte("seq_scan"))||bytes.Contains(emitted,[]byte("last_seq_scan")){t.Fatal("runtime monitoring serialized")}
		tableStatCreate(t,p,b,c,"sat_control_rows");tableStatSeed(t,p,b,"sat_control_rows");fixture.StatisticsExec(t,p,b,c,"SET stats_fetch_consistency=none",0)
		w,retire:=fixture.StatisticsReportingConn(t,p,b);fixture.RunStep(t,p.Ctx,w,b.Recorder,fixture.Select("SELECT payload FROM sat_control_rows WHERE n=2",[]string{"payload"},[]string{"TEXT"},[][]any{{nil}}));retire()
		old:=tableStatAwait(t,p,b,"sat_control_rows","destination positive history",func(r tableStatRow)bool{return r.Seq==1&&r.Read==3})
		keeper:=fixture.IdentityConnection(t,p.Ctx,b.DB)
		for _,step:=range []fixture.Step{fixture.Exec("CREATE TEMP TABLE sat_control_temp(n integer,payload text)",0),fixture.Exec("INSERT INTO sat_control_temp VALUES(1,NULL),(2,'b')",2),fixture.Exec("CREATE INDEX sat_temp_index ON sat_control_temp USING hash(n)",0),fixture.Select("SELECT n FROM sat_control_temp ORDER BY n",[]string{"n"},[]string{"INT4"},[][]any{{int64(1)},{int64(2)}})}{fixture.RunStep(t,p.Ctx,keeper,b.Recorder,step)}
		ctx,cancel:=context.WithTimeout(p.Ctx,fixture.Liveness);defer cancel();tick:=time.NewTicker(time.Millisecond);defer tick.Stop()
		var temp tableStatRow
		for{var one int;if err:=keeper.QueryRowContext(ctx,"SELECT 1").Scan(&one);err!=nil||one!=1{t.Fatal(err)};temp,err=tableStatRead(ctx,c,"sat_control_temp");if err!=nil{t.Fatal(err)};if temp.Seq==2&&temp.Inserts==2{break};select{case<-ctx.Done():t.Fatal("TEMP publication",temp,ctx.Err());case<-tick.C:}}
		if temp.Read!=4{t.Fatal("real TEMP build and query fetches",temp)}
		heldConn:=fixture.IdentityConnection(t,p.Ctx,b.DB)
		held,err:=heldConn.QueryContext(p.Ctx,tableStatSelect,"sat_control_temp")
		if err!=nil {t.Fatal(err)}
		defer held.Close()
		heldColumns,err:=held.ColumnTypes()
		if err!=nil {t.Fatal(err)}
		fixture.StatisticsExec(t,p,b,c,"SET track_counts=off",0)
		fixture.StatisticsExec(t,p,b,keeper,"SET track_counts=off",0)
		unchanged:=func() {
			t.Helper()
			if got:=tableStatGet(t,p,c,"sat_control_rows");!got.equal(old) {t.Fatal("failed Restore changed persistent identity/history",got,old)}
			if got:=tableStatGet(t,p,c,"sat_control_temp");!got.equal(temp) {t.Fatal("failed Restore changed TEMP identity/history",got,temp)}
			fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT n,payload FROM sat_control_rows ORDER BY n",[]string{"n","payload"},[]string{"INT4","TEXT"},[][]any{{int64(1),"a"},{int64(2),nil},{int64(3),"c"}}))
			fixture.RunStep(t,p.Ctx,keeper,b.Recorder,fixture.Select("SELECT n,payload FROM sat_control_temp ORDER BY n",[]string{"n","payload"},[]string{"INT4","TEXT"},[][]any{{int64(1),nil},{int64(2),"b"}}))
		}
		bad:=append(bytes.Clone(emitted),[]byte("\nCREATE TABLE sat_bad(n no_such_type);")...)
		if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(bad));err==nil{t.Fatal("late invalid Restore accepted")}
		unchanged()
		cancelled,stop:=context.WithCancel(p.Ctx);stop();if err:=stuntgres.Restore(cancelled,p.Native,bytes.NewReader(emitted));!errors.Is(err,context.Canceled){t.Fatal(err)}
		unchanged()
		during,cancelInput:=context.WithCancel(p.Ctx);input:=&indexStatCancelReader{Reader:bytes.NewReader(emitted),cancel:cancelInput};err=stuntgres.Restore(during,p.Native,input);cancelInput();if !errors.Is(err,context.Canceled)||!input.entered{t.Fatal("admitted external input cancellation",input.entered,err)}
		unchanged()
		if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(emitted));err!=nil{t.Fatal(err)}
		fresh:=tableStatGet(t,p,c,"sat_control_rows");if fresh.Inserts!=0||fresh.Seq!=0||fresh.Read!=0||fresh.LastSeq.Valid{t.Fatal("persistent history crossed replacement",fresh)}
		if got:=tableStatGet(t,p,c,"sat_control_temp");!got.equal(temp){t.Fatal("surviving TEMP did not transplant",got,temp)}
		if _,err:=tableStatRead(p.Ctx,c,"sat_source_temp");err!=sql.ErrNoRows{t.Fatal("foreign source TEMP imported",err)}
		if !held.Next() {t.Fatal("retained TEMP row missing",held.Err())}
		var retained tableStatRow
		if err:=held.Scan(retained.destinations()...);err!=nil {t.Fatal(err)}
		held.Close()
		if !retained.equal(temp){t.Fatal("retained cursor changed through replacement",retained,temp)}
		tableStatMetadata(t,heldColumns,false)
		fixture.StatisticsExec(t,p,b,c,"SET track_counts=off",0);fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT n,payload FROM sat_control_rows ORDER BY n",[]string{"n","payload"},[]string{"INT4","TEXT"},[][]any{{int64(1),"a"},{int64(2),nil},{int64(3),"c"}}))
		if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)}
		if _,err:=tableStatRead(p.Ctx,c,"sat_control_rows");err!=sql.ErrNoRows{t.Fatal("Reset retained persistent relation",err)}
		if got:=tableStatGet(t,p,c,"sat_control_temp");!got.equal(temp){t.Fatal("Reset lost attached TEMP history",got,temp)}
		if len(emitted)==0{t.Fatal("empty definitions/data dump")}
	})
}
