package core_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

func TestPGStatAllTablesParallelAccess(t *testing.T) {
	for _,tc:=range []struct{name,query string;aggregate,disabled bool;want []any}{
		{"query","SELECT n FROM sat_parallel",false,false,[]any{int64(1),int64(2),int64(3),int64(4)}},
		{"query_filtered","SELECT n FROM sat_parallel WHERE n=99",false,false,nil},
		{"aggregate","SELECT string_agg(n::text,',') FROM sat_parallel",true,false,[]any{"1","2","3","4"}},
		{"aggregate_filtered","SELECT string_agg(n::text,',') FROM sat_parallel WHERE n=99",true,false,[]any{nil}},
		{"query_disabled","SELECT n FROM sat_parallel",false,true,[]any{int64(1),int64(2),int64(3),int64(4)}},
		{"aggregate_disabled","SELECT string_agg(n::text,',') FROM sat_parallel",true,true,[]any{"1","2","3","4"}},
	} {
		t.Run(tc.name,func(t *testing.T) {
			e:=engine.New();setup:=enginetest.StartSession(t,e,nil)
			statsFunctionExec(t,setup,"CREATE TABLE sat_parallel(n integer,ok boolean)","INSERT INTO sat_parallel VALUES(1,TRUE),(2,TRUE),(3,TRUE),(4,TRUE)")
			statsEnd(t,setup)
			key:=statistics.Key{Object:enginetest.RoleTableRef(t,e,"sat_parallel")}
			s:=enginetest.StartSession(t,e,nil)
			if tc.disabled {statsFunctionExec(t,s,"SET track_counts=off")}
			query,source,release:=indexStatsCapture(t,e,s,tc.query)
			var rows exec.RowResult
			var count int
			var err error
			if tc.aggregate {
				aggregate,eligible:=exec.ParallelAggregatePlan(query.Root,source)
				if !eligible {t.Fatal("real aggregate source was not eligible")}
				rows,count,err=exec.FinalizeParallelAggregate(context.Background(),query.Root,aggregate,source,nil,2)
			} else {
				project,ordered,eligible:=exec.ParallelQueryPlan(query.Root,source)
				if !eligible {t.Fatal("real query source was not eligible")}
				rows,count,err=exec.FinalizeParallelQuery(context.Background(),project,ordered,source,nil,2)
			}
			wantRows:=len(tc.want);if tc.aggregate {wantRows=1}
			if err!=nil||rows==nil||count!=wantRows {t.Fatal("actual parallel result",count,tc.want,err)}
			tablesParallelValues(t,rows,tc.want,tc.aggregate)
			release();statsEnd(t,s)
			starts,tuples:=int64(2),int64(4)
			if tc.disabled {starts,tuples=0,0}
			tablesScanWant(t,e,key,starts,tuples,0)
			if f:=e.Statistics.Read(key);f.Inserts!=4||f.IndexScans!=0||f.VacuumCount!=0||f.AnalyzeCount!=0 {t.Fatal("parallel reduction changed other event families",f)}
		})
	}
	for _,aggregate:=range []bool{false,true} {
		for _,cancelled:=range []bool{false,true} {
			name:="query";if aggregate {name="aggregate"};if cancelled {name+="_canceled"} else {name+="_failed"}
			t.Run(name+"_fetched_prefix",func(t *testing.T){tablesParallelInterrupted(t,aggregate,cancelled,false)})
		}
	}
	t.Run("discarded_projected_output_preserves_full_read",func(t *testing.T){tablesParallelInterrupted(t,false,false,true)})
	for _,aggregate:=range []bool{false,true} {
		name:="query";if aggregate {name="aggregate"}
		t.Run(name+"_unstarted",func(t *testing.T) {
			e:=engine.New();s:=enginetest.StartSession(t,e,nil)
			statsFunctionExec(t,s,"CREATE TABLE sat_parallel(n integer)","INSERT INTO sat_parallel VALUES(1),(2),(3),(4)")
			q:="SELECT n FROM sat_parallel";if aggregate {q="SELECT string_agg(n::text,',') FROM sat_parallel"}
			query,source,release:=indexStatsCapture(t,e,s,q)
			key:=statistics.Key{Object:enginetest.RoleTableRef(t,e,"sat_parallel")}
			ctx,cancel:=context.WithCancel(context.Background());cancel()
			var rows exec.RowResult;var count int;var err error
			if aggregate {a,_:=exec.ParallelAggregatePlan(query.Root,source);rows,count,err=exec.FinalizeParallelAggregate(ctx,query.Root,a,source,nil,2)} else {rows,count,err=exec.FinalizeParallelQuery(ctx,query.Root.(*plan.ProjectNode),nil,source,nil,2)}
			if rows!=nil||count!=0||!errors.Is(err,context.Canceled) {t.Fatal("pre-start cancellation",rows,count,err)}
			release();statsEnd(t,s);tablesScanWant(t,e,key,0,0,0)
		})
	}
}

func tablesParallelValues(t *testing.T,rows exec.RowResult,want []any,aggregate bool) {
	t.Helper();defer rows.Close()
	values:=make([]sqltype.Value,len(rows.Schema()))
	gotBag,wantBag:=make(map[any]int),make(map[any]int)
	for _,value:=range want {wantBag[value]++}
	for {
		present,err:=rows.Next(context.Background(),values)
		if err!=nil {t.Fatal(err)}
		if !present {break}
		if aggregate&&!values[0].IsNull() {
			text,err:=values[0].Text();if err!=nil {t.Fatal(err)}
			// Unordered aggregate input/worker concatenation has no portable
			// order. Independently assert all actual tokens with multiplicity.
			for _,token:=range strings.Split(text,",") {gotBag[token]++}
		} else {gotBag[enginetest.Export(values[0])]++}
	}
	if !reflect.DeepEqual(gotBag,wantBag) {t.Fatal("actual parallel values",gotBag,wantBag)}
}

// Both actual lanes stop inside their first physical predicate (or after each
// lane's full drain at projection). No fabricated statistics or timing race is
// used to specify the reached prefix; existing typed evaluator seams own gates.
func tablesParallelInterrupted(t *testing.T,aggregate,cancelled,projection bool) {
	e:=engine.New();setup:=enginetest.StartSession(t,e,nil)
	statsFunctionExec(t,setup,"CREATE TABLE sat_parallel(n integer,ok boolean)","INSERT INTO sat_parallel VALUES(1,TRUE),(2,TRUE),(3,TRUE),(4,TRUE)")
	statsEnd(t,setup)
	key:=statistics.Key{Object:enginetest.RoleTableRef(t,e,"sat_parallel")}
	s:=enginetest.StartSession(t,e,nil)
	q:="SELECT n FROM sat_parallel WHERE ok"
	if aggregate {q="SELECT string_agg(n::text,',') FROM sat_parallel WHERE ok"}
	if projection {q="SELECT n FROM sat_parallel"}
	query,source,release:=indexStatsCapture(t,e,s,q)
	project:=query.Root.(*plan.ProjectNode)
	entered:=make(chan struct{},2);resume:=make(chan struct{})
	defer close(resume)
	sentinel:=errors.New("actual parallel scan predicate failure")
	probe:=func(c *exec.Context)(sqltype.Value,error) {
		select {case entered<-struct{}{}:case <-c.Context.Done():return sqltype.Value{},c.Context.Err()}
		if c.Query.ScanStart==0 {
			select {case <-resume:return sqltype.Value{},sentinel;case <-c.Context.Done():return sqltype.Value{},c.Context.Err()}
		}
		<-c.Context.Done()
		return sqltype.Value{},c.Context.Err()
	}
	var a *plan.AggregateNode
	if aggregate {a=project.Child.(*plan.AggregateNode)}
	if projection {project.Expressions[0]=enginetest.QueryProbe(project.Schema()[0].Usage,probe)} else {
		child:=project.Child;if aggregate {child=a.Child}
		filter:=child.(*plan.FilterNode)
		filter.Predicate=enginetest.QueryProbe(filter.Predicate.ResultUsage(),probe)
	}
	ctx,cancel,join:=parallelTestStart(t,func(ctx context.Context)parallelTestOutcome {
		var rows exec.RowResult;var count int;var err error
		if aggregate {rows,count,err=exec.FinalizeParallelAggregate(ctx,query.Root,a,source,nil,2)} else {rows,count,err=exec.FinalizeParallelQuery(ctx,project,nil,source,nil,2)}
		return parallelTestOutcome{rows,count,err}
	})
	for range 2 {select {case <-entered:case <-ctx.Done():t.Fatal("actual lanes did not reach defined prefix",ctx.Err())}}
	if cancelled {cancel()} else {resume<-struct{}{}}
	out:=join()
	if out.rows!=nil||out.count!=0||cancelled&&!errors.Is(out.err,context.Canceled)||!cancelled&&out.err!=sentinel {t.Fatal("parallel prefix failure identity",out)}
	release();statsEnd(t,s)
	tuples:=int64(2);if projection {tuples=4}
	tablesScanWant(t,e,key,2,tuples,0)
}
