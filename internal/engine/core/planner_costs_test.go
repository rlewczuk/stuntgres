package core_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqltype"
)

// Unlike the generic internal SQL fixture this goes through command admission.
// SQL results are reference-tested; these tests inspect retained runtime owners.
func plannerCostCommand(t *testing.T,s *session.Session,q string) core.Result {
	t.Helper()
	op,_,err:=s.AcquireCommand(context.Background()); if err!=nil { t.Fatal(err) }; defer op.Done()
	r,err:=core.EngineOf(s).Execute(op,enginetest.Statement(t,q),0,nil); if err!=nil { t.Fatal(q,err) }
	if r.Rows!=nil { t.Cleanup(func(){r.Rows.Close()}) }
	return r
}

func TestPlannerCostsHashExecution(t *testing.T) {
	e:=engine.New(); s:=enginetest.StartSession(t,e,nil)
	for _,q:=range []string{"CREATE TABLE t(n INT,s TEXT)","INSERT INTO t VALUES(2,'b'),(1,'a'),(2,'b'),(NULL,NULL),(3,'c')","CREATE INDEX planner_cost_hash_z ON t USING hash(n)","CREATE INDEX planner_cost_hash_a ON t USING hash(n)"} { plannerCostCommand(t,s,q) }
	selected,root:=hashRoot(t,e,"planner_cost_hash_z")
	alternative,otherRoot:=hashRoot(t,e,"planner_cost_hash_a")
	// Creation identity order deliberately opposes lexical name order.
	if selected.Address().ObjectID>=alternative.Address().ObjectID || selected.Name()<=alternative.Name() || root==otherRoot { t.Fatal("competing-index fixture lacks distinguishing identities") }
	for _,analyzed:=range []bool{false,true} {
	if analyzed { plannerCostCommand(t,s,"ANALYZE t") }
	for _,tc:=range []struct{value,label string; hash bool}{{"0.005","Hash Candidates",true},{"1000000","Full Scan",false},{"0.005","Hash Candidates",true}} {
		plannerCostCommand(t,s,"SET cpu_index_tuple_cost='"+tc.value+"'")
		r:=plannerCostCommand(t,s,"EXPLAIN SELECT n FROM t WHERE n=2")
		var text strings.Builder
		for _,row:=range enginetest.Tuples(r) { text.WriteString(row[0].(string)); text.WriteByte('\n') }
		if !strings.Contains(text.String(),"Access Method: "+tc.label) { t.Fatal(text.String()) }
		if tc.hash {
			if !strings.Contains(text.String(),"Index: planner_cost_hash_z\n") || strings.Contains(text.String(),"Index: planner_cost_hash_a\n") { t.Fatal("EXPLAIN did not choose smallest index identity",text.String()) }
		} else if strings.Contains(text.String(),"Index: ") { t.Fatal("full scan claims an index",text.String()) }
		cpu:="12.5"
		if analyzed { cpu="0.0625" }
		if tc.hash { cpu="10.0025"; if analyzed {cpu="0.0525"} }
		if !strings.Contains(text.String(),"CPU Cost: "+cpu+"\n") {t.Fatal("detached CPU estimate missing",text.String())}
		parent:=1e300
		for _,line:=range strings.Split(text.String(),"\n") {
			at:=strings.Index(line,"logical cost=")
			if at<0 {continue}
			var cost float64
			if _,err:=fmt.Sscanf(line[at:],"logical cost=%f",&cost);err!=nil || cost>parent {t.Fatal("noninclusive logical estimate",line,parent,err)}
			parent=cost
		}
		op,_,err:=s.AcquireCommand(context.Background()); if err!=nil { t.Fatal(err) }
		query,source,release:=hashCaptureQuery(t,e,op,"SELECT n FROM t WHERE n=2")
		if len(source.HashIndexes)!=2 || source.HashIndexes[0].Address!=selected.Address() || source.HashIndexes[0].Name!="planner_cost_hash_z" || source.HashIndexes[0].Root!=root || source.HashIndexes[1].Address!=alternative.Address() || source.HashIndexes[1].Name!="planner_cost_hash_a" || source.HashIndexes[1].Root!=otherRoot { t.Fatal("admitted competing indexes lost identity order",source.HashIndexes) }
		stage:=hashFilterStage(t,query,source,nil)
		if tc.hash {
			if source.ScanDecision.Method!=plan.HashCandidates || source.ScanDecision.Index!=selected.Address() || source.ScanDecision.IndexName!="planner_cost_hash_z" { t.Fatal("selected decision disagrees with EXPLAIN/captured root",source.ScanDecision) }
		} else if source.ScanDecision.Method!=plan.FullScan { t.Fatal("costly profile retained hash decision",source.ScanDecision) }
		if tc.hash { if stage.HashCandidates==nil || stage.HashCandidates.Root!=root || len(stage.HashCandidates.Occurrences)!=2 { t.Fatal("actual candidate branch missing",stage.HashCandidates) } } else if stage.HashCandidates!=nil { t.Fatal("full scan probed index",stage.HashCandidates) }
		if err:=stage.Drain(context.Background()); err!=nil || stage.Count()!=2 { t.Fatal(stage.Count(),err) }
		if tc.hash && stage.HashCandidates.Position!=2 { t.Fatal("candidate producer not consumed") }
		if analyzed && source.PlannerRows!=5 || !analyzed && source.PlannerRows!=1000 { t.Fatal("execution did not use detached maintenance estimate",source.PlannerRows) }
		stage.Close(); release(); op.Done()
	}
	}
}

func TestPlannerCostsAdmission(t *testing.T) {
	e:=engine.New(); s:=enginetest.StartSession(t,e,nil)
	plannerCostCommand(t,s,"SET cpu_tuple_cost='1.25'")
	op,_,err:=s.AcquireCommand(context.Background()); if err!=nil { t.Fatal(err) }
	admitted:=op.PlannerCosts
	s.Settings.Mu.Lock(); s.Settings.Values["cpu_tuple_cost"]="2"; s.Settings.Typed["cpu_tuple_cost"]=settings.Value{Number:2}; s.Settings.Mu.Unlock()
	if admitted.Tuple!=1.25 || op.PlannerCosts!=admitted { t.Fatal("mutable admission",admitted,op.PlannerCosts) }; op.Done()
	next,_,err:=s.AcquireCommand(context.Background()); if err!=nil { t.Fatal(err) }; if next.PlannerCosts.Tuple!=2 { t.Fatal(next.PlannerCosts) }; next.Done()
	for _,q:=range []string{"CREATE TABLE t(n INT)","INSERT INTO t VALUES(1),(2)","SET cpu_tuple_cost='0.01'"} { plannerCostCommand(t,s,q) }
	ast,d:=sequencePreparedDescription(t,e,s,"SELECT n FROM t")
	execute:=func(want float64) {
		op,_,err:=s.AcquireCommand(context.Background()); if err!=nil { t.Fatal(err) }; defer op.Done()
		scope,err:=e.StartStatement(op); if err!=nil { t.Fatal(err) }; scope.Expected=&d
		r,err:=scope.Execute(ast,0,nil); err=scope.Finish(err); if err!=nil { t.Fatal(err) }; defer r.Rows.Close()
		rows:=r.Rows.(*exec.FinalRows)
		if rows.Source==nil || rows.Source.PlannerCosts.Tuple!=want { t.Fatal("prepared retained old admission",rows.Source,want) }
	}
	execute(.01); plannerCostCommand(t,s,"SET cpu_tuple_cost='1.25'"); execute(1.25)
	plannerCostCommand(t,s,"BEGIN"); plannerCostCommand(t,s,"SAVEPOINT costs")
	plannerCostCommand(t,s,"SET LOCAL cpu_tuple_cost=3"); execute(3)
	plannerCostCommand(t,s,"DECLARE costs_cursor NO SCROLL CURSOR FOR SELECT n FROM t")
	cursor:=core.PayloadOf(s).Cursors.Entries["costs_cursor"]
	costs,decision:=cursor.Source.PlannerCosts,cursor.Source.ScanDecision
	plannerCostCommand(t,s,"SET LOCAL cpu_tuple_cost=4")
	if cursor.Source.PlannerCosts!=costs || cursor.Source.ScanDecision!=decision || costs.Tuple!=3 { t.Fatal("retained cursor borrowed settings") }
	plannerCostCommand(t,s,"CLOSE costs_cursor"); plannerCostCommand(t,s,"ROLLBACK TO costs"); execute(1.25); plannerCostCommand(t,s,"ROLLBACK")
	plannerCostCommand(t,s,"BEGIN")
	cycle:=s.NewProtocolCycle()
	command:=func(fn func(*core.StatementScope)error){preparedFoldProtocolCommand(t,e,s,cycle,fn)}
	command(func(scope *core.StatementScope)error{_,err:=scope.ParseProtocol("cost_definition","SELECT n FROM t",nil);return err})
	plannerCostCommand(t,s,"SET LOCAL cpu_tuple_cost=5")
	command(func(scope *core.StatementScope)error{_,err:=scope.BindProtocolPortal("cost_portal","cost_definition",&protocolUnitInput{data:[]byte{0,0,0,0,0,0}});return err})
	portal:=core.ProtocolPortalOf(core.PayloadOf(s).PreparedSQL.Binding("cost_portal"))
	producer:=core.PortalProducer(portal)
	if producer==nil || producer.Source.PlannerCosts.Tuple!=5 {t.Fatal("portal missed opening admission")}
	portalSource:=producer.Source
	portalDecision:=portalSource.ScanDecision
	plannerCostCommand(t,s,"SET LOCAL cpu_tuple_cost=6")
	command(func(scope *core.StatementScope)error{
		_,err:=scope.ExecuteProtocolPortal("cost_portal",1,func([]session.ProtocolField)error{return nil},nil,nil,core.CopyProtocolHooks{})
		return err
	})
	if producer.Source!=portalSource || portalSource.PlannerCosts.Tuple!=5 || portalSource.ScanDecision!=portalDecision {t.Fatal("portal resume recaptured settings/source")}
	command(func(scope *core.StatementScope)error{return scope.CloseProtocolPortal("cost_portal")})
	plannerCostCommand(t,s,"ROLLBACK")
	// A retained decision cannot contain any source graph, store or owner pointer.
	typ:=reflect.TypeFor[plan.ScanDecision]()
	for i:=range typ.NumField() { k:=typ.Field(i).Type.Kind(); if k==reflect.Pointer || k==reflect.Interface || k==reflect.Map || k==reflect.Slice { t.Fatal("decision retains authority",typ.Field(i)) } }
}

func TestPlannerCostsParallelExecution(t *testing.T) {
	for _,profile:=range []string{"cheap","setup","delivery","size"} {
		t.Run(profile,func(t *testing.T) {
			_,query,source,_,probe:=aggregateCombineParallelFixture(t)
			switch profile { case "setup":source.PlannerCosts.ParallelSetup=1000000; case "delivery":source.PlannerCosts.ParallelTuple=1000000; case "size":source.PlannerCosts.MinParallelTableBlocks=715827882 }
			entered:=make(chan *exec.State,2); release:=make(chan struct{}); var once sync.Once; defer once.Do(func(){close(release)})
			var mu sync.Mutex; states:=map[*exec.State]bool{}; serial:=0
			probe.advance=func(ctx context.Context,_ any,_ []sqltype.Value,evaluation expr.Context)error {
				if !exec.InParallelWorker(ctx) { mu.Lock(); serial++; mu.Unlock(); return nil }
				c:=evaluation.(*exec.Context)
				mu.Lock(); first:=!states[c.Query]; states[c.Query]=true; mu.Unlock()
				if first {
					select { case entered<-c.Query: case <-ctx.Done():return ctx.Err() }
					select { case <-release: case <-ctx.Done():return ctx.Err() }
				}
				return nil
			}
			ctx,_,join:=parallelTestStart(t,func(ctx context.Context)parallelTestOutcome {rows,n,err:=exec.FinalizeSelect(ctx,query.Root,source,nil,2);return parallelTestOutcome{rows,n,err}})
			if profile=="cheap" {
				var workers []*exec.State
				for range 2 { select {case w:=<-entered:workers=append(workers,w);case <-ctx.Done():t.Fatal("no real workers",ctx.Err())} }
				if workers[0]==workers[1] {t.Fatal("shared worker")}
				once.Do(func(){close(release)})
			}
			out:=join(); if out.err!=nil || out.count!=1 {t.Fatal(out.count,out.err)}
			got:=aggregateCombineString(t,out.rows)
			counts:=map[string]int{};for _,v:=range strings.Split(got,",") {counts[v]++}
			if !reflect.DeepEqual(counts,map[string]int{"1":1,"2":1,"3":1,"4":1}) {t.Fatal(got)}
			mu.Lock(); defer mu.Unlock()
			if profile=="cheap" {if len(states)!=2 || serial!=0 {t.Fatal(len(states),serial)}} else if len(states)!=0 || serial!=4 {t.Fatal("costly profile launched workers",len(states),serial)}
		})
	}
	for _,profile:=range []string{"cheap","setup","delivery","size"} {
		t.Run("query_"+profile,func(t *testing.T) {
			_,query,source:=queryCoreFixture(t,"SELECT id FROM t")
			switch profile {case "setup":source.PlannerCosts.ParallelSetup=1000000;case "delivery":source.PlannerCosts.ParallelTuple=1000000;case "size":source.PlannerCosts.MinParallelTableBlocks=715827882}
			rows,n,err:=exec.FinalizeSelect(context.Background(),query.Root,source,nil,2)
			if err!=nil || n!=4 {t.Fatal(n,err)}
			final:=rows.(*exec.FinalRows)
			// Unfiltered serial projection borrows the all-occurrences prefix;
			// the genuine query gather owns its explicit worker-run selection.
			if final.Source!=source || (final.Selection!=nil)!=(profile=="cheap") {t.Fatal("wrong query branch owner",profile,final.Source,final.Selection)}
			got:=parallelTestIDs(t,rows)
			counts:=map[int64]int{};for _,v:=range got {counts[v]++}
			if !reflect.DeepEqual(counts,map[int64]int{1:1,2:1,3:1,4:1}) {t.Fatal(got)}
		})
	}
}
