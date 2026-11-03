package core_test

import (
	"context"
	"reflect"
	"strings"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func TestPGStatAllTablesCatalogIdentity(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	statsFunctionExec(t, s, "CREATE TABLE sat_catalog(n integer)", "CREATE TABLE sat_noindex(n integer)", "SET track_counts=off", "CREATE INDEX sat_a ON sat_catalog(n)", "CREATE INDEX sat_b ON sat_catalog(n)", "SET track_counts=on", "SET stats_fetch_consistency=none")
	read := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatAllTablesOID}
	view := read.MustRelation(address)
	if pgcatalog.StatAllTablesOID != 12146 || view.RowType != 12148 || !view.VirtualView || len(view.Columns) != 30 { t.Fatal("exact ALL identity", view) }
	if _, exists := read.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12009}); exists { t.Fatal("obsolete ALL alias") }
	registered := 0
	for _, spec := range pgcatalog.Specs { if spec.Name == "pg_stat_all_tables" { registered++; if spec.OID != 12146 { t.Fatal("source registration", spec) } } }
	if registered != 1 { t.Fatal("source multiplicity", registered) }
	names := strings.Fields("relid schemaname relname seq_scan last_seq_scan seq_tup_read idx_scan last_idx_scan idx_tup_fetch n_tup_ins n_tup_upd n_tup_del n_tup_hot_upd n_tup_newpage_upd n_live_tup n_dead_tup n_mod_since_analyze n_ins_since_vacuum last_vacuum last_autovacuum last_analyze last_autoanalyze vacuum_count autovacuum_count analyze_count autoanalyze_count total_vacuum_time total_autovacuum_time total_analyze_time total_autoanalyze_time")
	for i, c := range view.Columns {
		kind := sqltype.Int8
		switch i { case 0: kind = sqltype.Oid; case 1,2: kind = sqltype.Name; case 4,7,18,19,20,21: kind = sqltype.Timestamptz; case 26,27,28,29: kind = sqltype.Float8 }
		if c.Name != names[i] || c.Usage.Type.ID() != kind || !c.Nullable || !c.NullabilityKnown || c.Usage.Typmod != -1 || c.OriginTable != 12146 || c.OriginAttribute != int16(i+1) { t.Fatal("typed column", i, c) }
	}
	want := [][]any{{int64(12146), int64(11), "v", int64(12148), int64(12147), int64(12148)}}
	got := indexStatsRows(t, s, "SELECT c.oid,c.relnamespace,c.relkind,c.reltype,t.typarray,a.typelem FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid=12146")
	if !reflect.DeepEqual(got, want) { t.Fatal("row/array owning relationships", got, want) }
	if def := pgcatalog.SummaryViewDefinition(pgcatalog.StatAllTablesOID); !strings.Contains(def, "GROUP BY") || !strings.Contains(def, "sum(") || !strings.Contains(def, "max(") { t.Fatal("released grouped definition", def) }
	key := statistics.Key{Object: enginetest.RoleTableRef(t, e, "sat_catalog")}
	a, b := statistics.Key{Object: enginetest.RoleTableRef(t, e, "sat_a")}, statistics.Key{Object: enginetest.RoleTableRef(t, e, "sat_b")}
	t1 := time.Date(2026,1,1,0,0,0,0,time.UTC)
	var w statistics.Writer
	w.Add(&e.Statistics, key, statistics.Fact{SeqScans: 1, SeqTuples: 3, TableIndexFetches: 7})
	w.IndexAccess(&e.Statistics, a, statistics.Policy{Counts:true}, 2, 4, 4)
	w.TransactionStopped(t1)
	w.Flush(&e.Statistics, t1, true)
	w.IndexAccess(&e.Statistics, b, statistics.Policy{Counts:true}, 3, 6, 6)
	w.TransactionStopped(t1.Add(time.Second))
	w.Flush(&e.Statistics, t1.Add(2*time.Second), true)
	e.Statistics.ReportAnalyze(key, statistics.Policy{Counts:true}, t1, 1500*time.Microsecond, 2, 1, true)
	rows := indexStatsRows(t, s, "SELECT seq_scan,seq_tup_read,idx_scan,last_idx_scan,idx_tup_fetch,total_analyze_time,n_tup_hot_upd,n_tup_newpage_upd FROM pg_stat_all_tables WHERE relname='sat_catalog'")
	if len(rows)!=1 || !reflect.DeepEqual(rows[0], []any{int64(1),int64(3),int64(5),t1.Add(time.Second),int64(17),float64(1.5),int64(0),int64(0)}) { t.Fatal("SUM/MAX/bitmap/ms aggregation", rows) }
	rows = indexStatsRows(t, s, "SELECT seq_scan,last_seq_scan,seq_tup_read,idx_scan,last_idx_scan,idx_tup_fetch FROM pg_stat_all_tables WHERE relname='sat_noindex'")
	if !reflect.DeepEqual(rows, [][]any{{int64(0),nil,int64(0),nil,nil,nil}}) { t.Fatal("fresh no-index NULL semantics", rows) }
	statsFunctionExec(t, s, "CREATE INDEX sat_noindex_a ON sat_noindex(n)", "CREATE INDEX sat_noindex_b ON sat_noindex(n)")
	rows = indexStatsRows(t, s, "SELECT idx_scan,last_idx_scan,idx_tup_fetch FROM pg_stat_all_tables WHERE relname='sat_noindex'")
	if !reflect.DeepEqual(rows, [][]any{{int64(0),nil,int64(0)}}) { t.Fatal("untouched attached indexes", rows) }
	t.Run("predicate_before_reader", tablesSourceSelection)
}

func tablesSourceSelection(t *testing.T) {
	for _, column := range []int{0,1,2} { t.Run([]string{"relid","schema","name"}[column], func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t,e,nil)
		statsFunctionExec(t,s,"CREATE SCHEMA sat_other", "CREATE TABLE sat_x(n integer)", "CREATE TABLE sat_other.sat_y(n integer)", "SET track_counts=off", "CREATE INDEX sat_xi ON sat_x(n)", "CREATE INDEX sat_yi ON sat_other.sat_y(n)", "SET track_counts=on")
		x,y := enginetest.RoleTableRef(t,e,"sat_x"), enginetest.RoleTableRef(t,e,"sat_other.sat_y")
		yi := enginetest.RoleTableRef(t,e,"sat_other.sat_yi")
		configureStats(s,"cache")
		enginetest.TransactionBegin(t,s)
		op := enginetest.TransactionOperation(t,s); defer op.Done()
		scope,err:=e.StartStatement(op); if err!=nil {t.Fatal(err)}; defer scope.Finish(nil)
		e.Mu.Lock(); auth,err:=e.AuthorizationLocked(op); e.Mu.Unlock(); if err!=nil {t.Fatal(err)}
		read:=auth.Catalog
		a:=ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:pgcatalog.StatAllTablesOID}
		runtime:=core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine:e,Auth:auth,Settings:s.Settings})
		in:=pgcatalog.Input{Context:context.Background(),Catalog:read,Roles:auth.Roles,Owner:&e.Catalog.Owner,Ref:e.Catalog.Domain.Ref(read.Epoch(),a),Source:read.MustRelation(a),Runtime:runtime}
		in.Keep=pgcatalog.Keep{Column:column,Test:func(v sqltype.Value)bool {if column==0 {n,_:=v.OID(); return ids.OID(n)==x.Address.ObjectID}; text,_:=v.Text(); if column==1{return text=="public"};return text=="sat_x"}}
		produce,_:=pgcatalog.ProducerFor(pgcatalog.StatAllTablesOID)
		if _,err:=produce(in);err!=nil{t.Fatal(err)}
		var w statistics.Writer
		w.Add(&e.Statistics,statistics.Key{Object:y},statistics.Fact{SeqScans:2,SeqTuples:6})
		w.IndexAccess(&e.Statistics,statistics.Key{Object:yi},statistics.Policy{Counts:true},3,4,4)
		w.Flush(&e.Statistics,time.Now(),true)
		if f:=runtime.TableStatistics(y.Address);f.SeqScans!=2||f.SeqTuples!=6{t.Fatal("rejected table pre-cached",f)}
		if f:=runtime.IndexStatistics(yi.Address);f.Scans!=3||f.Fetches!=4{t.Fatal("rejected attached index pre-cached",f)}
	}) }
}

func TestPGStatAllTablesCurrentTransaction(t *testing.T) {
	for _, commit := range []bool{false,true} {t.Run(map[bool]string{false:"rollback",true:"commit"}[commit],func(t *testing.T){
		e:=engine.New();s:=enginetest.StartSession(t,e,nil)
		statsFunctionExec(t,s,"CREATE TABLE sat_current(n integer,payload text)","BEGIN","INSERT INTO sat_current VALUES(1,'a'),(2,NULL)")
		key:=statistics.Key{Object:enginetest.RoleTableRef(t,e,"sat_current")}
		owner:=s.Transactions
		if got:=owner.Stats.CurrentCounts(key,nil);got!=(statistics.TupleCounts{Inserts:2}){t.Fatal("unresolved current ownership",got)}
		if f:=e.Statistics.Read(key);f.Inserts!=0{t.Fatal("unresolved cumulative leak",f)}
		end:="ROLLBACK";if commit{end="COMMIT"};statsFunctionExec(t,s,end)
		if got:=owner.Stats.CurrentCounts(key,nil);got!=(statistics.TupleCounts{})||len(owner.Stats)!=0{t.Fatal("ended transaction retained counts",got,owner.Stats)}
		statsEnd(t,s)
		want:=[7]int64{2,0,0,0,2,0,2};count:=0;if commit{want=[7]int64{2,0,0,2,0,2,2};count=2}
		f:=e.Statistics.Read(key);if got:=[7]int64{f.Inserts,f.Updates,f.Deletes,f.Live,f.Dead,f.Modifications,f.InsertsSince};got!=want{t.Fatal("settlement",got,want)}
		reader:=enginetest.StartSession(t,e,nil)
		if rows:=enginetest.Tuples(enginetest.SessionSQL(t,reader,"SELECT n FROM sat_current"));len(rows)!=count{t.Fatal("physical transaction result",rows)}
	})}
}
