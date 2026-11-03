package core_test

import (
	"context"
	"errors"
	"fmt"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

func tablesOpen(t *testing.T, query *plan.Query, source *exec.Source) exec.Stage {
	t.Helper()
	exec.AdmitScan(query.Root,source)
	state:=&exec.State{Source:source,HashFilter:exec.HashQueryFilter(query.Root),Frame:query.Root.(*plan.ProjectNode).Frame}
	if scope:=core.ScopeOf(source.LiveHost());scope!=nil{state.Input,state.Output=scope.Input,scope.Output}
	stage,err:=exec.OpenNode(context.Background(),state,query.Root)
	if err!=nil{t.Fatal(err)}
	return stage
}

func tablesScanWant(t *testing.T,e *core.Engine,key statistics.Key,scans,tuples,bitmap int64){
	t.Helper(); f:=e.Statistics.Read(key)
	if f.SeqScans!=scans||f.SeqTuples!=tuples||f.TableIndexFetches!=bitmap||(scans==0)!=f.LastSeqScan.IsZero(){t.Fatalf("physical scan %+v want %d/%d/%d",f,scans,tuples,bitmap)}
}

func TestPGStatAllTablesScanDemand(t *testing.T){
	for _,tc:=range []struct{name,query string; scans,tuples int64; rows int}{
		{"filter","SELECT payload FROM sat_demand WHERE n=2",1,3,1},
		{"no_match","SELECT payload FROM sat_demand WHERE n=99",1,3,0},
		{"empty","SELECT payload FROM sat_empty",1,0,0},
		{"limit_zero","SELECT payload FROM sat_demand LIMIT 0",0,0,0},
		{"unused","SELECT payload FROM sat_demand WHERE FALSE",0,0,0},
		{"limit_one","SELECT payload FROM sat_demand LIMIT 1",1,1,1},
		{"exists","SELECT EXISTS(SELECT n FROM sat_demand)",1,1,1},
		{"blocking","SELECT payload FROM sat_demand ORDER BY n LIMIT 1",1,3,1},
		{"aliases","SELECT a.n,b.n FROM sat_demand a CROSS JOIN sat_demand b",2,6,9},
		{"retained_cte","WITH x AS MATERIALIZED(SELECT n FROM sat_demand) SELECT a.n,b.n FROM x a CROSS JOIN x b",1,3,9},
	}{t.Run(tc.name,func(t *testing.T){
		e:=engine.New();setup:=enginetest.StartSession(t,e,nil)
		statsFunctionExec(t,setup,"CREATE TABLE sat_demand(n integer,payload text)","CREATE TABLE sat_empty(n integer,payload text)","INSERT INTO sat_demand VALUES(1,'a'),(2,NULL),(3,'c')")
		name:="sat_demand";if tc.name=="empty"{name="sat_empty"}
		key:=statistics.Key{Object:enginetest.RoleTableRef(t,e,name)};statsEnd(t,setup)
		s:=enginetest.StartSession(t,e,nil)
		query,source,release:=indexStatsCapture(t,e,s,tc.query)
		stage:=tablesOpen(t,query,source)
		if err:=stage.Drain(context.Background());err!=nil||stage.Count()!=tc.rows{t.Fatal("actual demand rows",stage.Count(),tc.rows,err)}
		if tc.name=="exists" {
			// The singleton outer source is not the scalar EXISTS demand.
			var values [1]sqltype.Value
			if present,err:=stage.ProjectOrdinal(context.Background(),0,values[:]);err!=nil||!present {t.Fatal("actual EXISTS projection",present,err)}
			if value,err:=values[0].Boolean();err!=nil||!value {t.Fatal("actual EXISTS result",value,err)}
		}
		stage.Close();release();statsEnd(t,s)
		tablesScanWant(t,e,key,tc.scans,tc.tuples,0)
	})}
	t.Run("copied_wrappers_replay_and_real_reopen",func(t *testing.T){
		e:=engine.New();s:=enginetest.StartSession(t,e,nil)
		statsFunctionExec(t,s,"CREATE TABLE sat_replay(n integer)","INSERT INTO sat_replay VALUES(1),(2),(3)")
		key:=statistics.Key{Object:enginetest.RoleTableRef(t,e,"sat_replay")}
		query,source,release:=indexStatsCapture(t,e,s,"SELECT n FROM sat_replay")
		stage:=tablesOpen(t,query,source)
		if _,ok,err:=stage.NextOccurrence(context.Background());err!=nil||!ok{t.Fatal(ok,err)}
		copied:=stage
		if ok,err:=copied.ProjectOrdinal(context.Background(),0,nil);err!=nil||!ok{t.Fatal(ok,err)}
		if _,err:=stage.Rewind(context.Background());err!=nil{t.Fatal(err)}
		if _,ok,err:=stage.NextOccurrence(context.Background());err!=nil||!ok{t.Fatal(ok,err)}
		// A fresh physical evaluation is a new invocation, not retained rewind.
		reopened:=tablesOpen(t,query,source)
		if _,ok,err:=reopened.NextOccurrence(context.Background());err!=nil||!ok{t.Fatal(ok,err)}
		reopened.Close();stage.Close();release();statsEnd(t,s)
		tablesScanWant(t,e,key,2,2,0)
	})
	t.Run("physical_hierarchy_and_pruned_empties",func(t *testing.T){
		e:=engine.New();s:=enginetest.StartSession(t,e,nil)
		statsFunctionExec(t,s,"CREATE TABLE sat_parent(n integer)","CREATE TABLE sat_leading() INHERITS(sat_parent)","CREATE TABLE sat_excluded(CHECK(n<10)) INHERITS(sat_parent)","CREATE TABLE sat_trailing() INHERITS(sat_parent)","INSERT INTO sat_excluded VALUES(1),(2),(3)")
		query,source,release:=indexStatsCapture(t,e,s,"SELECT n FROM sat_parent WHERE n=99")
		stage:=tablesOpen(t,query,source)
		if err:=stage.Drain(context.Background());err!=nil||stage.Count()!=0{t.Fatal(stage.Count(),err)}
		stage.Close();release();statsEnd(t,s)
		for _,name:=range []string{"sat_parent","sat_leading","sat_trailing"}{tablesScanWant(t,e,statistics.Key{Object:enginetest.RoleTableRef(t,e,name)},1,0,0)}
		tablesScanWant(t,e,statistics.Key{Object:enginetest.RoleTableRef(t,e,"sat_excluded")},0,0,0)
	})
	t.Run("real_index_and_lossy_bitmap",func(t *testing.T){
		e,s,index,a:=indexStatsBloomSetup(t)
		statsEnd(t,s)
		s=enginetest.StartSession(t,e,nil)
		statsFunctionExec(t,s,"SET enable_seqscan=off")
		key:=statistics.Key{Object:enginetest.RoleTableRef(t,e,"stat_bloom")}
		query,source,release:=indexStatsCapture(t,e,s,fmt.Sprintf("SELECT n FROM stat_bloom WHERE n=%d",a))
		stage:=hashFilterStage(t,query,source,nil)
		if source.ScanDecision.Method!=plan.BloomCandidates||stage.HashCandidates==nil||len(stage.HashCandidates.Occurrences)!=3{t.Fatal("real lossy source not selected")}
		if err:=stage.Drain(context.Background());err!=nil||stage.Count()!=2{t.Fatal(stage.Count(),err)}
		stage.Close();release();statsEnd(t,s)
		tablesScanWant(t,e,key,1,0,3);indexStatsWant(t,e,index,1,3,0)
	})
	t.Run("selected_empty_branch_matrix",tablesSelectedEmptyBranches)
	t.Run("partition_physical_leaves",tablesPartitionScans)
	t.Run("mixed_hierarchy_unopened_foreign_arm",tablesMixedHierarchyScans)
	t.Run("rls_fetches_before_rejection",func(t *testing.T){
		e:=engine.New();s:=enginetest.StartSession(t,e,nil)
		statsFunctionExec(t,s,"CREATE TABLE sat_rls(n integer)","INSERT INTO sat_rls VALUES(1),(2),(3)","CREATE ROLE sat_rls_reader","GRANT USAGE ON SCHEMA public TO sat_rls_reader","GRANT SELECT ON sat_rls TO sat_rls_reader","CREATE POLICY sat_policy ON sat_rls USING(n=99)","ALTER TABLE sat_rls ENABLE ROW LEVEL SECURITY","SET ROLE sat_rls_reader")
		key:=statistics.Key{Object:enginetest.RoleTableRef(t,e,"sat_rls")}
		query,source,release:=indexStatsCapture(t,e,s,"SELECT n FROM sat_rls")
		if source.RLS==nil { t.Fatal("actual RLS source was not admitted") }
		stage:=tablesOpen(t,query,source)
		if err:=stage.Drain(context.Background());err!=nil||stage.Count()!=0 { t.Fatal(stage.Count(),err) }
		stage.Close();release();statsEnd(t,s)
		tablesScanWant(t,e,key,1,3,0)
	})
	t.Run("dml_uses_same_heap_frontier",func(t *testing.T){
		e:=engine.New();setup:=enginetest.StartSession(t,e,nil)
		statsFunctionExec(t,setup,"CREATE TABLE sat_dml(n integer)","INSERT INTO sat_dml VALUES(1),(2),(3)");statsEnd(t,setup)
		s:=enginetest.StartSession(t,e,nil);key:=statistics.Key{Object:enginetest.RoleTableRef(t,e,"sat_dml")}
		statsFunctionExec(t,s,"UPDATE sat_dml SET n=n WHERE n=99","DELETE FROM sat_dml WHERE n=99");statsEnd(t,s)
		tablesScanWant(t,e,key,2,6,0)
		if f:=e.Statistics.Read(key);f.Updates!=0||f.Deletes!=0{t.Fatal("no-match fabricated tuple events",f)}
	})
}

func TestPGStatAllTablesInterruptedAccess(t *testing.T){
	t.Run("predicate_error_retains_fetched_prefix",func(t *testing.T){
		e:=engine.New();s:=enginetest.StartSession(t,e,nil)
		statsFunctionExec(t,s,"CREATE TABLE sat_error(n integer)","INSERT INTO sat_error VALUES(1),(2),(3)","BEGIN")
		key:=statistics.Key{Object:enginetest.RoleTableRef(t,e,"sat_error")}
		query,source,release:=indexStatsCapture(t,e,s,"SELECT n FROM sat_error WHERE n>0")
		filter:=exec.HashQueryFilter(query.Root)
		calls:=0;primary:=errors.New("second fetched predicate failed")
		filter.Predicate=tableScanFailure{Expr:filter.Predicate,calls:&calls,failure:primary}
		stage:=hashFilterStage(t,query,source,nil)
		if err:=stage.Drain(context.Background());err!=primary { t.Fatal("primary executor failure replaced",err) }
		stage.Close();release()
		statsFunctionExec(t,s,"ROLLBACK");statsEnd(t,s)
		if calls!=2 { t.Fatal("predicate read an unfetched tail",calls) }
		tablesScanWant(t,e,key,1,2,0)
	})
	for _,disabled:=range []bool{false,true}{t.Run(fmt.Sprint(disabled),func(t *testing.T){
		e:=engine.New();setup:=enginetest.StartSession(t,e,nil)
		statsFunctionExec(t,setup,"CREATE TABLE sat_interrupt(n integer)","INSERT INTO sat_interrupt VALUES(1),(2),(3)");statsEnd(t,setup)
		s:=enginetest.StartSession(t,e,nil);key:=statistics.Key{Object:enginetest.RoleTableRef(t,e,"sat_interrupt")}
		if disabled{statsFunctionExec(t,s,"SET track_counts=off")}
		statsFunctionExec(t,s,"BEGIN")
		query,source,release:=indexStatsCapture(t,e,s,"SELECT n FROM sat_interrupt")
		stage:=tablesOpen(t,query,source)
		for range 2{if _,ok,err:=stage.NextOccurrence(context.Background());err!=nil||!ok{t.Fatal(ok,err)}}
		ctx,cancel:=context.WithCancel(context.Background());cancel()
		if _,_,err:=stage.NextOccurrence(ctx);!errors.Is(err,context.Canceled){t.Fatal("interrupted identity",err)}
		stage.Close();release()
		if f:=e.Statistics.Read(key);f.SeqScans!=0||f.SeqTuples!=0{t.Fatal("published inside transaction",f)}
		statsFunctionExec(t,s,"ROLLBACK");statsEnd(t,s)
		scans,tuples:=int64(1),int64(2);if disabled{scans,tuples=0,0}
		tablesScanWant(t,e,key,scans,tuples,0)
	})}
}

type tableScanFailure struct {
	expr.Expr
	calls *int
	failure error
}

func (e tableScanFailure) Eval(c expr.Context)(sqltype.Value,error) {
	*e.calls++
	if *e.calls==2 { return sqltype.Value{},e.failure }
	return e.Expr.Eval(c)
}

func tablesSelectedEmptyBranches(t *testing.T) {
	for _,tc:=range []struct{name,query string; rows int; reached []string}{
		{"full","SELECT n FROM sat_matrix WHERE n=99",1,[]string{"sat_matrix","sat_before","sat_middle","sat_row","sat_after"}},
		{"early_stop","SELECT n FROM sat_matrix WHERE n=99 LIMIT 1",1,[]string{"sat_matrix","sat_before","sat_middle","sat_row"}},
		{"only_empty","SELECT n FROM sat_matrix WHERE n=199",0,[]string{"sat_matrix","sat_before","sat_middle","sat_after"}},
		{"unused","SELECT n FROM sat_matrix WHERE n=99 LIMIT 0",0,nil},
		{"unpruned","SELECT n FROM sat_matrix",1,[]string{"sat_matrix","sat_before","sat_excluded_empty","sat_middle","sat_row","sat_after"}},
	} {
		t.Run(tc.name,func(t *testing.T){
			e:=engine.New()
			s:=enginetest.StartSession(t,e,nil)
			statsFunctionExec(t,s,
				"CREATE TABLE sat_matrix(n integer)",
				"CREATE TABLE sat_before() INHERITS(sat_matrix)",
				"CREATE TABLE sat_excluded_empty(CHECK(n<10)) INHERITS(sat_matrix)",
				"CREATE TABLE sat_middle() INHERITS(sat_matrix)",
				"CREATE TABLE sat_row(CHECK(n>=90 AND n<100)) INHERITS(sat_matrix)",
				"CREATE TABLE sat_after() INHERITS(sat_matrix)",
				"INSERT INTO sat_row VALUES(99)")
			query,source,release:=indexStatsCapture(t,e,s,tc.query)
			stage:=tablesOpen(t,query,source)
			if err:=stage.Drain(context.Background());err!=nil||stage.Count()!=tc.rows { t.Fatal("unchanged pruned row bag",stage.Count(),tc.rows,err) }
			stage.Close();release();statsEnd(t,s)
			for _,name:=range []string{"sat_matrix","sat_before","sat_excluded_empty","sat_middle","sat_row","sat_after"} {
				scans,tuples:=int64(0),int64(0)
				for _,reached:=range tc.reached { if reached==name { scans=1;if name=="sat_row" {tuples=1} } }
				tablesScanWant(t,e,statistics.Key{Object:enginetest.RoleTableRef(t,e,name)},scans,tuples,0)
			}
		})
	}
}

func tablesPartitionScans(t *testing.T) {
	for _,tc:=range []struct{name,query string; rows int; first,second [2]int64}{
		{"nonempty_selected","SELECT n FROM sat_partition_scan WHERE n=2",1,[2]int64{1,3},[2]int64{}},
		{"empty_selected","SELECT n FROM sat_partition_scan WHERE n=12",0,[2]int64{},[2]int64{1,0}},
		{"unreached","SELECT n FROM sat_partition_scan LIMIT 0",0,[2]int64{},[2]int64{}},
	} {
		t.Run(tc.name,func(t *testing.T) {
			e:=engine.New()
			s:=enginetest.StartSession(t,e,nil)
			statsFunctionExec(t,s,"CREATE TABLE sat_partition_scan(n integer) PARTITION BY RANGE(n)","CREATE TABLE sat_partition_one PARTITION OF sat_partition_scan FOR VALUES FROM(0) TO(10)","CREATE TABLE sat_partition_two PARTITION OF sat_partition_scan FOR VALUES FROM(10) TO(20)","INSERT INTO sat_partition_scan VALUES(1),(2),(3)")
			query,source,release:=indexStatsCapture(t,e,s,tc.query)
			stage:=tablesOpen(t,query,source)
			if err:=stage.Drain(context.Background());err!=nil||stage.Count()!=tc.rows {t.Fatal("partition source rows",stage.Count(),tc.rows,err)}
			stage.Close();release();statsEnd(t,s)
			for _,physical:=range []struct{name string;want [2]int64}{{"sat_partition_scan",[2]int64{}},{"sat_partition_one",tc.first},{"sat_partition_two",tc.second}} {
				tablesScanWant(t,e,statistics.Key{Object:enginetest.RoleTableRef(t,e,physical.name)},physical.want[0],physical.want[1],0)
			}
		})
	}
}

// The existing unit wrapper is solely an unopened foreign-arm control. All
// counted work belongs to genuine local engine heaps; no foreign row/transport
// is consumed or claimed by this proof.
func tablesMixedHierarchyScans(t *testing.T) {
	for _,tc:=range []struct{name string;parentRows,unused bool}{
		{"local_prefix",true,false},
		{"empty_then_local",false,false},
		{"unused",false,true},
	} {
		t.Run(tc.name,func(t *testing.T) {
			e,s,wrapper:=explainExecutionFixture(t)
			statsFunctionExec(t,s,"CREATE TABLE sat_mixed(k integer,v integer)","CREATE TABLE sat_mixed_local() INHERITS(sat_mixed)","INSERT INTO sat_mixed_local VALUES(1,10),(2,20),(3,30)","CREATE FOREIGN TABLE sat_mixed_remote() INHERITS(sat_mixed) SERVER executed_explain_server")
			if tc.parentRows {statsFunctionExec(t,s,"INSERT INTO sat_mixed VALUES(4,40),(5,50),(6,60)")}
			q:="SELECT k FROM sat_mixed LIMIT 1"
			if tc.unused {q="SELECT k FROM sat_mixed LIMIT 0"}
			query,source,release:=indexStatsCapture(t,e,s,q)
			if len(source.Inheritance)!=3||source.Inheritance[2].Prefix.Foreign==nil {t.Fatal("actual mixed hierarchy not captured",source.Inheritance)}
			stage:=tablesOpen(t,query,source)
			wantRows:=1
			if tc.unused {wantRows=0}
			if err:=stage.Drain(context.Background());err!=nil||stage.Count()!=wantRows {t.Fatal("local demanded prefix",stage.Count(),wantRows,err)}
			stage.Close();release();statsEnd(t,s)
			if wrapper.begins!=0 {t.Fatal("unopened foreign arm consumed unit data",wrapper.begins)}
			parentScans,parentRead,childScans,childRead:=int64(1),int64(0),int64(1),int64(1)
			if tc.parentRows {parentRead,childScans,childRead=1,0,0}
			if tc.unused {parentScans,parentRead,childScans,childRead=0,0,0,0}
			tablesScanWant(t,e,statistics.Key{Object:enginetest.RoleTableRef(t,e,"sat_mixed")},parentScans,parentRead,0)
			tablesScanWant(t,e,statistics.Key{Object:enginetest.RoleTableRef(t,e,"sat_mixed_local")},childScans,childRead,0)
		})
	}
}
