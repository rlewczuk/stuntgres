package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func cteDMLRoutes(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, bool)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) { body(t, p, b, prepared) })
			}
		})
	}
}

func cteDMLRun(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool, s fixture.Step) {
	t.Helper()
	if !prepared {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		return
	}
	if s.State != "" && s.ErrorStage == "prepare" {
		s.Prepared = true
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		return
	}
	stmt, err := b.DB.PrepareContext(p.Ctx, s.Query)
	if err != nil {
		t.Fatal("Prepare", s.Query, err)
	}
	defer stmt.Close()
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: s.Query}, b.Recorder, s)
}

func cteDMLBag(q string, names, types []string, rows [][]any) fixture.Step {
	s := fixture.Select(q, names, types, rows)
	s.Bag = true
	return s
}

func cteDMLSeed(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE t(id INT,v INT)", 0), fixture.Exec("INSERT INTO t VALUES(1,10),(2,20)", 2)} {
		cteDMLRun(t, p, b, false, s)
	}
}

func TestReferenceCTEDMLBodies(t *testing.T) {
	for _, kind := range []string{"insert", "update", "delete", "merge"} {
		t.Run(kind, func(t *testing.T) {
			cteDMLRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := func(s fixture.Step) { cteDMLRun(t, p, b, false, s) }
				run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
				q := "INSERT INTO t VALUES(1,10),(2,NULL) RETURNING id,v"
				want := [][]any{{int64(1), int64(10)}, {int64(2), nil}}
				final := want
				if kind != "insert" {
					run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20)", 2))
				}
				switch kind {
				case "update":
					q = "UPDATE t SET v=v+1 RETURNING id,v"
					want = [][]any{{int64(1), int64(11)}, {int64(2), int64(21)}}
					final = want
				case "delete":
					q = "DELETE FROM t RETURNING id,v"
					want = [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}
					final = [][]any{}
				case "merge":
					run(fixture.Exec("CREATE TABLE s(sid INT,delta INT)", 0))
					run(fixture.Exec("INSERT INTO s VALUES(1,7),(2,8),(4,9),(5,0),(6,0)", 5))
					run(fixture.Exec("INSERT INTO t VALUES(3,30),(5,50)", 2))
					q = mergeReturningMixed + " RETURNING merge_action() AS action,old.id AS old_id,old.v AS old_v,new.id AS new_id,new.v AS new_v"
					cteDMLRun(t, p, b, prepared, cteDMLBag("WITH m AS ("+q+") SELECT * FROM m", []string{"action", "old_id", "old_v", "new_id", "new_v"}, []string{"TEXT", "INT4", "INT4", "INT4", "INT4"}, [][]any{{"DELETE", int64(1), int64(10), nil, nil}, {"DELETE", int64(3), int64(30), nil, nil}, {"UPDATE", int64(2), int64(20), int64(2), int64(28)}, {"INSERT", nil, nil, int64(4), int64(9)}}))
					run(mergeReturningSurvivors())
					return
				}
				cteDMLRun(t, p, b, prepared, cteDMLBag("WITH d AS ("+q+") SELECT * FROM d", []string{"id", "v"}, []string{"INT4", "INT4"}, want))
				run(cteDMLBag("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, final))
			})
		})
	}
}

func TestReferenceCTEDMLPipeline(t *testing.T) {
	cteDMLRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := func(s fixture.Step) { cteDMLRun(t, p, b, false, s) }
		run(fixture.Exec("CREATE TABLE src(id INT,v INT)", 0))
		run(fixture.Exec("CREATE TABLE dst(id INT,v INT)", 0))
		run(fixture.Exec("INSERT INTO src VALUES(1,10),(2,NULL),(2,NULL)", 3))
		bag := [][]any{{int64(1), int64(10)}, {int64(2), nil}, {int64(2), nil}}
		q := "WITH moved AS (DELETE FROM src RETURNING id,v), projected AS (SELECT id,v FROM moved), copied AS (INSERT INTO dst SELECT id,v FROM projected RETURNING id,v) SELECT * FROM copied"
		cteDMLRun(t, p, b, prepared, cteDMLBag(q, []string{"id", "v"}, []string{"INT4", "INT4"}, bag))
		run(cteDMLBag("SELECT * FROM src", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{}))
		run(cteDMLBag("SELECT * FROM dst", []string{"id", "v"}, []string{"INT4", "INT4"}, bag))
	})
}

func TestReferenceCTEDMLReuse(t *testing.T) {
	for _, kind := range []string{"insert", "update", "delete", "merge"} {
		t.Run(kind, func(t *testing.T) {
			cteDMLRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				cteDMLSeed(t, p, b)
				body := "UPDATE t SET v=v+1 RETURNING id,v"
				want := [][]any{{int64(1), int64(11), int64(11)}, {int64(2), int64(21), int64(21)}}
				final := [][]any{{int64(1), int64(11)}, {int64(2), int64(21)}}
				switch kind {
				case "insert":
					body = "INSERT INTO t VALUES(3,30),(4,40) RETURNING id,v"
					want = [][]any{{int64(3), int64(30), int64(30)}, {int64(4), int64(40), int64(40)}}
					final = [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), int64(30)}, {int64(4), int64(40)}}
				case "delete":
					body = "DELETE FROM t RETURNING id,v"
					want = [][]any{{int64(1), int64(10), int64(10)}, {int64(2), int64(20), int64(20)}}
					final = [][]any{}
				case "merge":
					body = "MERGE INTO t USING (VALUES(1),(2)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=t.v+1 RETURNING t.id,t.v"
				}
				cteDMLRun(t, p, b, prepared, cteDMLBag("WITH u AS ("+body+") SELECT a.id,a.v,b.v FROM u a JOIN u b ON a.id=b.id", []string{"id", "v", "v"}, []string{"INT4", "INT4", "INT4"}, want))
				cteDMLRun(t, p, b, false, cteDMLBag("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, final))
			})
		})
	}
}

func TestReferenceCTEDMLHints(t *testing.T) {
	for _, hint := range []string{"", "MATERIALIZED ", "NOT MATERIALIZED "} {
		t.Run(hint, func(t *testing.T) {
			cteDMLRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				cteDMLSeed(t, p, b)
				cteDMLRun(t, p, b, false, fixture.Exec("CREATE SEQUENCE s", 0))
				q := "WITH u AS " + hint + "(UPDATE t SET v=v+1 RETURNING id,v), unused AS " + hint + "(UPDATE t SET v=v WHERE id=99 RETURNING nextval('s')) SELECT a.id,a.v,b.v FROM u a JOIN u b ON a.id=b.id"
				cteDMLRun(t, p, b, prepared, cteDMLBag(q, []string{"id", "v", "v"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(11), int64(11)}, {int64(2), int64(21), int64(21)}}))
				q = "WITH unused_select AS (SELECT nextval('s')), u AS " + hint + "(UPDATE t SET v=v RETURNING nextval('s')) SELECT 7 AS n"
				cteDMLRun(t, p, b, prepared, fixture.Select(q, []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}}))
				cteDMLRun(t, p, b, false, fixture.Select("SELECT last_value,is_called FROM s", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(2), true}}))
				cteDMLRun(t, p, b, prepared, fixture.Select("WITH d AS "+hint+"(DELETE FROM t) SELECT 7 AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}}))
				cteDMLRun(t, p, b, false, cteDMLBag("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{}))
			})
		})
	}
}
