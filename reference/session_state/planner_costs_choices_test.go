package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func (s *physicalCase) plannerCostSeed() {
	for _,q:=range []string{"CREATE TABLE planner_cost_rows(n integer,s text)","CREATE INDEX planner_cost_hash ON planner_cost_rows USING hash(n)"} { s.run(fixture.Exec(q,0)) }
	s.run(fixture.Exec("INSERT INTO planner_cost_rows VALUES(2,'b'),(1,'a'),(2,'b'),(NULL,NULL),(3,'c')",5))
	for _,q:=range []string{"SET cpu_tuple_cost='0.01'","SET cpu_operator_cost='0.0025'","SET cpu_index_tuple_cost='0.005'","SET max_parallel_workers_per_gather=0"} { s.run(fixture.Exec(q,0)) }
}

func plannerCostBag(query string,args ...any) fixture.Step {
	step:=fixture.Select(query,[]string{"n","s"},[]string{"INT4","TEXT"},[][]any{{int64(2),"b"},{int64(2),"b"}},args...); step.Bag=true; return step
}

func (s *physicalCase) plannerCostExplain(hash bool, prepared bool) string {
	s.t.Helper()
	query:="EXPLAIN SELECT n,s FROM planner_cost_rows WHERE n=2"
	var rows *sql.Rows; var err error
	if prepared { stmt,e:=s.conn.PrepareContext(s.p.Ctx,query); if e!=nil { s.t.Fatal(e) }; defer stmt.Close(); rows,err=stmt.QueryContext(s.p.Ctx) } else { rows,err=s.conn.QueryContext(s.p.Ctx,query) }
	if err!=nil { s.t.Fatal(err) }; defer rows.Close()
	var text strings.Builder
	for rows.Next() { var line string; if err:=rows.Scan(&line); err!=nil { s.t.Fatal(err) }; text.WriteString(line); text.WriteByte('\n') }
	if err:=rows.Err(); err!=nil { s.t.Fatal(err) }
	plan:=text.String()
	if s.pg {
		if !strings.Contains(plan,"Scan") || !strings.Contains(plan,"planner_cost_rows") || strings.Contains(plan,"Access Method:") { s.t.Fatal("not a PostgreSQL access plan",plan) }
	} else {
		want:="Access Method: Full Scan"; reject:="Access Method: Hash Candidates"
		if hash { want,reject=reject,want }
		if !strings.Contains(plan,want) || strings.Contains(plan,reject) || hash && !strings.Contains(plan,"Index: planner_cost_hash") { s.t.Fatal("wrong admitted access method",plan) }
		cpu:="12.5"; if hash {cpu="10.0025"}
		if !strings.Contains(plan,"CPU Cost: "+cpu+"\n") {s.t.Fatal("wrong detached CPU estimate",plan)}
	}
	return plan
}

func TestReferencePlannerCostsChoices(t *testing.T) {
	physicalCases(t,nil,func(s *physicalCase) {
		s.plannerCostSeed()
		const query="SELECT n,s FROM planner_cost_rows WHERE n=$1"
		held,err:=s.conn.PrepareContext(s.p.Ctx,query); if err!=nil { s.t.Fatal(err) }; defer held.Close()
		check:=func(hash bool) {
			for _,prepared:=range []bool{false,true} {
				s.plannerCostExplain(hash,prepared)
				step:=plannerCostBag("SELECT n,s FROM planner_cost_rows WHERE n=2"); step.Prepared=prepared; s.run(step)
			}
			fixture.RunStep(s.t,s.p.Ctx,fixture.HeldStatement{Executor:s.conn,Statement:held,SQL:query},s.recorder,plannerCostBag(query,2))
			for _,tc:=range []struct{arg any; rows [][]any}{{1,[][]any{{int64(1),"a"}}},{nil,nil}} {
				step:=fixture.Select(query,[]string{"n","s"},[]string{"INT4","TEXT"},tc.rows,tc.arg)
				fixture.RunStep(s.t,s.p.Ctx,fixture.HeldStatement{Executor:s.conn,Statement:held,SQL:query},s.recorder,step)
			}
		}
		for _,value:=range []string{"0.005","1000000","0.005"} { s.plannerCostSet("cpu_index_tuple_cost",value,false); check(value=="0.005") }
		for _,prefix:=range []string{"SET LOCAL","SET"} {
			s.run(fixture.Exec("BEGIN",0)); s.run(fixture.Exec(prefix+" cpu_index_tuple_cost=1000000",0)); check(false)
			s.run(fixture.Exec("SAVEPOINT planner_choice",0)); s.run(fixture.Exec("SET cpu_index_tuple_cost='0.005'",0)); check(false)
			s.run(fixture.Exec("ROLLBACK TO planner_choice",0)); check(false)
			s.run(fixture.Exec("ROLLBACK",0)); check(true)
		}
		for _,predicate:=range []string{"n=2 OR n=99","n=2 AND s='b'","n::bigint=2::bigint"} {
			s.run(plannerCostBag("SELECT n,s FROM planner_cost_rows WHERE "+predicate))
		}
	})
}

func TestReferencePlannerSeqscanChoices(t *testing.T) {
	physicalCases(t,nil,func(s *physicalCase) {
		s.plannerCostSeed()
		// Eliminate PostgreSQL's separate bitmap alternative so the independent
		// properties below distinguish ordinary index scans from full scans.
		for _,q:=range []string{"SET enable_bitmapscan=off","SET cpu_index_tuple_cost=1000000"} { s.run(fixture.Exec(q,0)) }
		const query="SELECT n,s FROM planner_cost_rows WHERE n=$1"
		const explain="EXPLAIN SELECT n,s FROM planner_cost_rows WHERE n=2"
		const nullQuery="SELECT n,s FROM planner_cost_rows WHERE s IS NULL"
		held,err:=s.conn.PrepareContext(s.p.Ctx,query); if err!=nil { s.t.Fatal(err) }; defer held.Close()
		heldExplain,err:=s.conn.PrepareContext(s.p.Ctx,explain); if err!=nil { s.t.Fatal(err) }; defer heldExplain.Close()
		heldNull,err:=s.conn.PrepareContext(s.p.Ctx,nullQuery); if err!=nil { s.t.Fatal(err) }; defer heldNull.Close()
		checkPlan:=func(prepared,hash bool) {
			var rows *sql.Rows; var err error
			if prepared { rows,err=heldExplain.QueryContext(s.p.Ctx) } else { rows,err=s.conn.QueryContext(s.p.Ctx,explain) }
			if err!=nil { s.t.Fatal(err) }; defer rows.Close()
			var text strings.Builder
			for rows.Next() { var line string; if err:=rows.Scan(&line); err!=nil { s.t.Fatal(err) }; text.WriteString(line); text.WriteByte('\n') }
			if err:=rows.Err(); err!=nil { s.t.Fatal(err) }
			plan:=text.String()
			want,reject:="Access Method: Full Scan","Access Method: Hash Candidates"
			if s.pg { want,reject="Seq Scan on planner_cost_rows","Index Scan using planner_cost_hash" }
			if hash { want,reject=reject,want }
			if !strings.Contains(plan,want) || strings.Contains(plan,reject) { s.t.Fatal("wrong sequential-scan preference",plan) }
			if !s.pg {
				cpu:="12.5"; if hash { cpu="5e+08" }
				if !strings.Contains(plan,"CPU Cost: "+cpu+"\n") || hash && !strings.Contains(plan,"Index: planner_cost_hash\n") { s.t.Fatal("preference lost cost/index facts",plan) }
			}
		}
		for _,profile:=range []struct{seq,index string; hash bool}{
			{"on","on",false},{"off","on",true},{"off","off",false},{"on","on",false},{"off","on",true},
		} {
			s.run(fixture.Exec("SET enable_seqscan="+profile.seq,0))
			s.run(fixture.Exec("SET enable_indexscan="+profile.index,0))
			for _,prepared:=range []bool{false,true} {
				checkPlan(prepared,profile.hash)
				step:=plannerCostBag("SELECT n,s FROM planner_cost_rows WHERE n=2"); step.Prepared=prepared; s.run(step)
				// No index exists for s, and a NULL equality probe must not
				// manufacture a matching tuple or lose the stored NULL row.
				nulls:=fixture.Select("SELECT n,s FROM planner_cost_rows WHERE s IS NULL",[]string{"n","s"},[]string{"INT4","TEXT"},[][]any{{nil,nil}}); nulls.Prepared=prepared; s.run(nulls)
				empty:=fixture.Select(query,[]string{"n","s"},[]string{"INT4","TEXT"},nil,nil); empty.Prepared=prepared; s.run(empty)
			}
			for _,tc:=range []struct{arg any; rows [][]any}{
				{2,[][]any{{int64(2),"b"},{int64(2),"b"}}},{1,[][]any{{int64(1),"a"}}},{nil,nil},
			} {
				step:=fixture.Select(query,[]string{"n","s"},[]string{"INT4","TEXT"},tc.rows,tc.arg); step.Bag=true
				fixture.RunStep(s.t,s.p.Ctx,fixture.HeldStatement{Executor:s.conn,Statement:held,SQL:query},s.recorder,step)
			}
			fixture.RunStep(s.t,s.p.Ctx,fixture.HeldStatement{Executor:s.conn,Statement:heldNull,SQL:nullQuery},s.recorder,fixture.Select(nullQuery,[]string{"n","s"},[]string{"INT4","TEXT"},[][]any{{nil,nil}}))
		}
	})
}

func (s *physicalCase) plannerCostParallel() {
	s.run(fixture.Exec("CREATE TABLE planner_parallel(n integer)",0)); s.run(fixture.Exec("INSERT INTO planner_parallel VALUES(1),(2),(3),(4)",4))
	query:="SELECT n FROM planner_parallel"
	held,err:=s.conn.PrepareContext(s.p.Ctx,query); if err!=nil { s.t.Fatal(err) }; defer held.Close()
	for _,profile:=range []struct{setup,delivery,size string}{{"0","0","0"},{"1000000","0","0"},{"0","1000000","0"},{"0","0","715827882"},{"0","0","0"}} {
		for _,q:=range []string{"SET max_parallel_workers_per_gather=2","SET parallel_setup_cost="+profile.setup,"SET parallel_tuple_cost="+profile.delivery,"SET min_parallel_table_scan_size="+profile.size} { s.run(fixture.Exec(q,0)) }
		step:=fixture.Select(query,[]string{"n"},[]string{"INT4"},[][]any{{int64(1)},{int64(2)},{int64(3)},{int64(4)}}); step.Bag=true; s.run(step)
		fixture.RunStep(s.t,s.p.Ctx,fixture.HeldStatement{Executor:s.conn,Statement:held,SQL:query},s.recorder,step)
		s.run(fixture.Select("SELECT count(*) AS c,sum(n) AS s FROM planner_parallel",[]string{"c","s"},[]string{"INT8","INT8"},[][]any{{int64(4),int64(10)}}))
	}
	s.run(fixture.Exec("BEGIN",0)); s.run(fixture.Exec("SET LOCAL min_parallel_table_scan_size=715827882",0)); s.run(fixture.Exec("ROLLBACK",0))
	s.resourceValue("min_parallel_table_scan_size","0","0","session",true)
}

func TestReferencePlannerCostsParallel(t *testing.T) { physicalCases(t,nil,func(s *physicalCase) { s.plannerCostParallel() }) }
