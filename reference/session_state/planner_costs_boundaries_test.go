package reference_test

import (
	"bytes"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePlannerCostsBoundaries(t *testing.T) {
	physicalCases(t,nil,func(s *physicalCase) {
		s.plannerCostSeed()
		for _,cost:=range []string{"0.005","1000000"} {
			s.plannerCostSet("cpu_index_tuple_cost",cost,false)
			for _,tc:=range []struct{name,input,show,raw string}{
				{"seq_page_cost","0","0","0"},{"random_page_cost","0.5","0.5","0.5"},
				{"effective_cache_size","1GB","1GB","131072"},{"min_parallel_index_scan_size","0","0","0"},
				{"min_parallel_index_scan_size","715827882","5726623056kB","715827882"},
				{"jit_above_cost","0","0","0"},{"jit_above_cost","-1","-1","-1"},{"jit_above_cost","-0.5","-0.5","-0.5"},
				{"jit_inline_above_cost","0","0","0"},{"jit_inline_above_cost","-1","-1","-1"},{"jit_inline_above_cost","-0.5","-0.5","-0.5"},
				{"jit_optimize_above_cost","0","0","0"},{"jit_optimize_above_cost","-1","-1","-1"},{"jit_optimize_above_cost","-0.5","-0.5","-0.5"},
			} {
				s.plannerCostSet(tc.name,tc.input,true); s.resourceValue(tc.name,tc.show,tc.raw,"session",true)
				text:=s.plannerCostExplain(cost=="0.005",false)
				if !s.pg && (strings.Contains(text,"JIT:") || strings.Contains(text,"Workers Planned:") || strings.Contains(text,"Index Scan")) { s.t.Fatal("fictional physical mechanism",text) }
				s.run(plannerCostBag("SELECT n,s FROM planner_cost_rows WHERE n=2"))
			}
		}
		if s.pg { return }
		var dump bytes.Buffer
		if err:=stuntgres.Dump(s.p.Ctx,s.p.Native,&dump); err!=nil { s.t.Fatal(err) }
		for _,d:=range plannerCostSettings { if strings.Contains(dump.String(),"SET "+d.name) { s.t.Fatal("serialized session setting",d.name) } }
		retained,err:=s.conn.QueryContext(s.p.Ctx,"SELECT n,s FROM planner_cost_rows WHERE n=2"); if err!=nil { s.t.Fatal(err) }; defer retained.Close()
		if err:=stuntgres.Reset(s.p.Ctx,s.p.Native); err!=nil { s.t.Fatal(err) }
		s.resourceValue("cpu_index_tuple_cost","1e+06","1e+06","session",true)
		if err:=stuntgres.Restore(s.p.Ctx,s.p.Native,bytes.NewReader(dump.Bytes())); err!=nil { s.t.Fatal(err) }
		s.resourceValue("cpu_index_tuple_cost","1e+06","1e+06","session",true)
		count:=0
		for retained.Next() { var n int64; var value string; if err:=retained.Scan(&n,&value); err!=nil || n!=2 || value!="b" { s.t.Fatal(n,value,err) }; count++ }
		if err:=retained.Err(); err!=nil || count!=2 { s.t.Fatal(count,err) }; retained.Close()
		s.run(plannerCostBag("SELECT n,s FROM planner_cost_rows WHERE n=2"))
	})
}

func TestReferencePlannerCostsFeatureSmoke(t *testing.T) {
	physicalCases(t,[]string{"planner_cost_smoke_login"},func(s *physicalCase) {
		s.plannerCostInventory(false)
		s.run(fixture.Exec("CREATE ROLE planner_cost_smoke_login LOGIN PASSWORD 'role-core-fixture'",0))
		ordinary:=s.login("planner_cost_smoke_login"); ordinary.plannerCostSet("cpu_tuple_cost","1.25",true); ordinary.resourceValue("cpu_tuple_cost","1.25","1.25","session",true)
		s.plannerCostSeed()
		const query="SELECT n,s FROM planner_cost_rows WHERE n=$1"
		held,err:=s.conn.PrepareContext(s.p.Ctx,query); if err!=nil { s.t.Fatal(err) }; defer held.Close()
		check:=func(hash bool) { s.plannerCostExplain(hash,false); s.run(plannerCostBag("SELECT n,s FROM planner_cost_rows WHERE n=2")); fixture.RunStep(s.t,s.p.Ctx,fixture.HeldStatement{Executor:s.conn,Statement:held,SQL:query},s.recorder,plannerCostBag(query,2)) }
		check(true); s.plannerCostSet("cpu_index_tuple_cost","1000000",false); check(false); s.plannerCostSet("cpu_index_tuple_cost","0.005",true); check(true)
		s.run(fixture.Exec("BEGIN",0)); s.run(fixture.Exec("SET LOCAL cpu_index_tuple_cost=1000000",0)); check(false); s.run(fixture.Exec("ROLLBACK",0)); check(true)
		d:=plannerCostSettings[3]
		s.run(physicalError("SET effective_cache_size=0",plannerCostBounds(d,"0"),true,false)); s.plannerCostValue(d,"4GB","524288","default",true)
		s.plannerCostSet("jit_above_cost","-0.5",true); s.resourceValue("jit_above_cost","-0.5","-0.5","session",true)
		s.plannerCostParallel()
	})
}
