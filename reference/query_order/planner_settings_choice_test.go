package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

type plannerQueryCase struct {
	t *testing.T
	p *fixture.Pair
	b fixture.Backend
	conn *sql.Conn
}

func plannerQueryCases(t *testing.T, body func(*plannerQueryCase)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil { t.Fatal(err) }; defer conn.Close()
			body(&plannerQueryCase{t,p,b,conn})
		})
	}
}
func (s *plannerQueryCase) run(step fixture.Step) { s.t.Helper(); fixture.RunStep(s.t,s.p.Ctx,s.conn,s.b.Recorder,step) }
func (s *plannerQueryCase) exec(q string) { s.t.Helper(); s.run(fixture.Exec(q,0)) }
func (s *plannerQueryCase) plan(q, native, pg string, prepared bool) {
	s.t.Helper()
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, failure := s.conn.PrepareContext(s.p.Ctx, "EXPLAIN " + q)
		if failure != nil { s.t.Fatal(failure) }; defer stmt.Close()
		rows, err = stmt.QueryContext(s.p.Ctx)
	} else { rows, err = s.conn.QueryContext(s.p.Ctx, "EXPLAIN " + q) }
	if err != nil { s.t.Fatal(err) }; defer rows.Close()
	var lines []string
	for rows.Next() { var line string; if err := rows.Scan(&line); err != nil { s.t.Fatal(err) }; lines = append(lines,line) }
	if err := rows.Err(); err != nil { s.t.Fatal(err) }
	want := native; if s.b.PG { want = pg }
	if !strings.Contains(strings.Join(lines,"\n"),want) { s.t.Fatalf("plan lacks %q:\n%s",want,strings.Join(lines,"\n")) }
}
func (s *plannerQueryCase) toggles(name, query string, columns, types []string, want [][]any, bag bool) {
	s.t.Helper()
	stmt, err := s.conn.PrepareContext(s.p.Ctx,query)
	if err != nil { s.t.Fatal(err) }; defer stmt.Close()
	for _, value := range []string{"on","off","on"} {
		s.exec("SET " + name + "=" + value)
		step := fixture.Select(query,columns,types,want); step.Bag = bag
		s.run(step)
		fixture.RunStep(s.t,s.p.Ctx,fixture.HeldStatement{Executor:s.conn,Statement:stmt,SQL:query},s.b.Recorder,step)
	}
}
func (s *plannerQueryCase) tables() {
	s.exec("CREATE TABLE pm_left(k integer,v text)")
	s.exec("CREATE TABLE pm_right(k integer,v text)")
	s.run(fixture.Exec("INSERT INTO pm_left VALUES(1,'a'),(1,'b'),(2,'c'),(NULL,'n')",4))
	s.run(fixture.Exec("INSERT INTO pm_right VALUES(1,'x'),(1,'y'),(3,'z'),(NULL,'m')",4))
}

func TestReferencePlannerSettingsJoinChoice(t *testing.T) {
	plannerQueryCases(t,func(s *plannerQueryCase) {
		s.tables()
		// Independent PG expectation: with merge and nested loop discouraged,
		// an admissible equality join selects Hash Join. With hash and merge
		// discouraged the required fallback is Nested Loop. No costs are goldens.
		s.exec("SET enable_mergejoin=off"); s.exec("SET enable_nestloop=off")
		q := "SELECT l.k,l.v,r.v FROM pm_left l JOIN pm_right r ON l.k=r.k"
		want := [][]any{{int64(1),"a","x"},{int64(1),"a","y"},{int64(1),"b","x"},{int64(1),"b","y"}}
		for _, prepared := range []bool{false,true} {
			s.exec("SET enable_hashjoin=on"); s.plan(q,"Method: hash-assisted","Hash Join",prepared)
			s.exec("SET enable_hashjoin=off"); s.plan(q,"Method: nested-loop","Nested Loop",prepared)
		}
		s.toggles("enable_hashjoin",q,[]string{"k","v","v"},[]string{"INT4","TEXT","TEXT"},want,true)
		s.exec("SET enable_hashjoin=off")
		step := fixture.Select("SELECT l.k,r.k FROM pm_left l JOIN pm_right r ON l.k<r.k",[]string{"k","k"},[]string{"INT4","INT4"},[][]any{{int64(1),int64(3)},{int64(1),int64(3)},{int64(2),int64(3)}}); step.Bag=true; s.run(step)
	})
}
func TestReferencePlannerSettingsAggregateChoice(t *testing.T) {
	plannerQueryCases(t,func(s *plannerQueryCase) {
		s.tables(); s.exec("SET max_parallel_workers_per_gather=0")
		group := "SELECT k,count(*) FROM pm_left GROUP BY k"
		distinct := "SELECT DISTINCT k FROM pm_left"
		for _, prepared := range []bool{false,true} {
			s.exec("SET enable_hashagg=on"); s.plan(group,"Method: hash grouping","HashAggregate",prepared); s.plan(distinct,"Method: hash deduplication","HashAggregate",prepared)
			s.exec("SET enable_hashagg=off"); s.plan(group,"Method: equality grouping","GroupAggregate",prepared); s.plan(distinct,"Method: equality deduplication","Unique",prepared)
		}
		s.toggles("enable_hashagg",group,[]string{"k","count"},[]string{"INT4","INT8"},[][]any{{int64(1),int64(2)},{int64(2),int64(1)},{nil,int64(1)}},true)
		s.toggles("enable_hashagg",distinct,[]string{"k"},[]string{"INT4"},[][]any{{int64(1)},{int64(2)},{nil}},true)
		for _, value := range []string{"on","off"} {
			s.exec("SET enable_hashagg="+value)
			step := fixture.Select("SELECT k,count(*) FROM pm_left GROUP BY GROUPING SETS ((k),(k),())",[]string{"k","count"},[]string{"INT4","INT8"},[][]any{{int64(1),int64(2)},{int64(1),int64(2)},{int64(2),int64(1)},{int64(2),int64(1)},{nil,int64(1)},{nil,int64(1)},{nil,int64(4)}}); step.Bag=true; s.run(step)
			s.run(fixture.Select("SELECT count(*) FROM pm_left WHERE false",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}))
			s.run(fixture.Select("SELECT k,count(*) FROM pm_left GROUP BY k HAVING count(*)>1",[]string{"k","count"},[]string{"INT4","INT8"},[][]any{{int64(1),int64(2)}}))
			s.run(fixture.Select("SELECT string_agg(DISTINCT v,',' ORDER BY v) FROM pm_left",[]string{"string_agg"},[]string{"TEXT"},[][]any{{"a,b,c,n"}}))
		}
	})
}
