package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Every route has its own literal expectations. No discovery backend filter is
// used: the pinned run also records native failures, without hiding PG subtests.
func insertReturningRoutes(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, bool)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) { body(t, p, b, prepared) })
			}
		})
	}
}
func insertReturningRun(t *testing.T, p *fixture.Pair, b fixture.Backend, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
}
func insertReturningBag(q string, names, types []string, rows [][]any, prepared bool) fixture.Step {
	s := fixture.Select(q, names, types, rows)
	s.Prepared = prepared
	s.Bag = true
	return s
}
func TestReferenceInsertReturningCore(t *testing.T) {
	insertReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := func(s fixture.Step) { insertReturningRun(t, p, b, s) }
		run(fixture.Exec("CREATE TABLE t(id INT PRIMARY KEY,v TEXT DEFAULT 'd')", 0))
		run(insertReturningBag("INSERT INTO t(id) VALUES(1),(2) RETURNING *,old.*,new.*", []string{"id", "v", "id", "v", "id", "v"}, []string{"INT4", "TEXT", "INT4", "TEXT", "INT4", "TEXT"}, [][]any{{int64(1), "d", nil, nil, int64(1), "d"}, {int64(2), "d", nil, nil, int64(2), "d"}}, prepared))
		run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "d"}, {int64(2), "d"}}))
		s := fixture.Select("INSERT INTO t SELECT 3,'unused' WHERE FALSE RETURNING *", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{})
		s.Prepared = prepared
		run(s)
	})
}
func TestReferenceInsertReturningCounts(t *testing.T) {
	insertReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := func(s fixture.Step) { insertReturningRun(t, p, b, s) }
		run(fixture.Exec("CREATE TABLE t(id INT PRIMARY KEY,v INT)", 0))
		for _, tc := range []struct {
			q string
			n int64
		}{
			{"INSERT INTO t VALUES(1,10),(2,20) RETURNING id", 2},
			{"INSERT INTO t VALUES(1,10) ON CONFLICT(id) DO UPDATE SET v=excluded.v RETURNING old.v,new.v", 1},
			{"INSERT INTO t VALUES(1,11) ON CONFLICT DO NOTHING RETURNING *", 0},
			{"INSERT INTO t VALUES(1,11) ON CONFLICT(id) DO UPDATE SET v=excluded.v WHERE FALSE RETURNING *", 0},
			{"INSERT INTO t SELECT 3,30 WHERE FALSE RETURNING *", 0},
		} {
			s := fixture.Exec(tc.q, tc.n)
			s.Prepared = prepared
			run(s)
		}
		run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
	})
}
func TestReferenceInsertReturningProducers(t *testing.T) {
	insertReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := func(s fixture.Step) { insertReturningRun(t, p, b, s) }
		run(fixture.Exec("CREATE TABLE t(id INT DEFAULT 2,v INT DEFAULT 7,g INT GENERATED ALWAYS AS (id+v) STORED)", 0))
		for _, q := range []string{"INSERT INTO t DEFAULT VALUES", "INSERT INTO t VALUES(2)", "INSERT INTO t(v,id) VALUES(7,2)", "INSERT INTO t(id) SELECT 2", "INSERT INTO t(id) VALUES(2) UNION ALL SELECT 2"} {
			expected := [][]any{{nil, int64(2), int64(7), int64(9)}}
			if q == "INSERT INTO t(id) VALUES(2) UNION ALL SELECT 2" {
				expected = append(expected, expected[0])
			}
			run(insertReturningBag(q+" RETURNING old.id,new.id,v,g", []string{"id", "id", "v", "g"}, []string{"INT4", "INT4", "INT4", "INT4"}, expected, prepared))
		}
		run(fixture.Exec("CREATE TABLE virtual_t(id INT,v INT DEFAULT 7,g INT GENERATED ALWAYS AS (id+v) VIRTUAL)", 0))
		run(insertReturningBag("INSERT INTO virtual_t(id) VALUES(2) RETURNING old.g,new.g,g", []string{"g", "g", "g"}, []string{"INT4", "INT4", "INT4"}, [][]any{{nil, int64(9), int64(9)}}, prepared))
		run(fixture.Exec("CREATE TABLE base(id INT,v TEXT,extra INT DEFAULT 99)", 0))
		run(fixture.Exec("CREATE VIEW live AS SELECT id,v FROM base", 0))
		run(insertReturningBag("INSERT INTO live VALUES(3,'view') RETURNING *,old.*,new.*", []string{"id", "v", "id", "v", "id", "v"}, []string{"INT4", "TEXT", "INT4", "TEXT", "INT4", "TEXT"}, [][]any{{int64(3), "view", nil, nil, int64(3), "view"}}, prepared))
		run(fixture.Exec("CREATE TABLE parts(id INT,v TEXT) PARTITION BY RANGE(id)", 0))
		run(fixture.Exec("CREATE TABLE low PARTITION OF parts FOR VALUES FROM (0) TO (10)", 0))
		run(fixture.Exec("CREATE TABLE high PARTITION OF parts FOR VALUES FROM (10) TO (20)", 0))
		run(insertReturningBag("INSERT INTO parts VALUES(12,'high'),(2,'low') RETURNING old.id,new.id,v", []string{"id", "id", "v"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{nil, int64(12), "high"}, {nil, int64(2), "low"}}, prepared))
		run(fixture.Exec("CREATE TABLE identities(id INT GENERATED ALWAYS AS IDENTITY,v TEXT DEFAULT 'identity')", 0))
		run(insertReturningBag("INSERT INTO identities DEFAULT VALUES RETURNING old.id,new.id,v", []string{"id", "id", "v"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{nil, int64(1), "identity"}}, prepared))
	})
}
func TestReferenceInsertReturningExpressions(t *testing.T) {
	insertReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		insertReturningRun(t, p, b, fixture.Exec("CREATE TABLE t(id INT,v TEXT DEFAULT 'd')", 0))
		s := fixture.Select("INSERT INTO t(id) VALUES(2) RETURNING id+1 AS \"After\",id AS dup,new.id AS dup,COALESCE(old.v,new.v),'x',CASE WHEN old.id IS NULL THEN new.id ELSE old.id END AS chosen", []string{"After", "dup", "dup", "coalesce", "?column?", "chosen"}, []string{"INT4", "INT4", "INT4", "TEXT", "TEXT", "INT4"}, [][]any{{int64(3), int64(2), int64(2), "d", "x", int64(2)}})
		s.Prepared = prepared
		insertReturningRun(t, p, b, s)
	})
}
func TestReferenceInsertReturningConflict(t *testing.T) {
	insertReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := func(s fixture.Step) { insertReturningRun(t, p, b, s) }
		run(fixture.Exec("CREATE TABLE t(id INT PRIMARY KEY,v INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
		run(insertReturningBag("INSERT INTO t VALUES(1,20),(2,30) ON CONFLICT(id) DO UPDATE SET v=t.v+excluded.v RETURNING old.v,new.v,v", []string{"v", "v", "v"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(10), int64(30), int64(30)}, {nil, int64(30), int64(30)}}, prepared))
		for _, pred := range []string{"FALSE", "NULL"} {
			run(insertReturningBag("INSERT INTO t VALUES(1,99),(3,40) ON CONFLICT(id) DO UPDATE SET v=excluded.v WHERE "+pred+" RETURNING old.v,new.v", []string{"v", "v"}, []string{"INT4", "INT4"}, [][]any{{nil, int64(40)}}, prepared))
			run(fixture.Exec("DELETE FROM t WHERE id=3", 1))
		}
		run(insertReturningBag("INSERT INTO t VALUES(1,99),(2,99) ON CONFLICT DO NOTHING RETURNING *", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{}, prepared))
		insertReturningFailure(t, p, b, b.DB, "INSERT INTO t VALUES(4,40),(1,99),(1,98) ON CONFLICT(id) DO UPDATE SET v=excluded.v RETURNING id", prepared, true, fixture.Diagnostic{Code: "21000", Message: "ON CONFLICT DO UPDATE command cannot affect row a second time", Hint: "Ensure that no rows proposed for insertion within the same command have duplicate constrained values."})
		run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(30)}, {int64(2), int64(30)}}))
	})
}
func TestReferenceInsertReturningFeatureSmoke(t *testing.T) {
	insertReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := func(s fixture.Step) { insertReturningRun(t, p, b, s) }
		run(fixture.Exec("CREATE TABLE t(id INT PRIMARY KEY,v TEXT DEFAULT 'd')", 0))
		run(insertReturningBag("INSERT INTO t(id) VALUES(1) RETURNING old.id,new.id,v", []string{"id", "id", "v"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{nil, int64(1), "d"}}, prepared))
		run(insertReturningBag("INSERT INTO t VALUES(1,'changed'),(2,'two') ON CONFLICT(id) DO UPDATE SET v=excluded.v RETURNING WITH(OLD AS o,NEW AS n) o.v,n.v", []string{"v", "v"}, []string{"TEXT", "TEXT"}, [][]any{{"d", "changed"}, {nil, "two"}}, prepared))
		run(insertReturningBag("INSERT INTO t VALUES(2,'skip') ON CONFLICT DO NOTHING RETURNING *", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{}, prepared))
		s := fixture.Exec("INSERT INTO t(id) VALUES(3) RETURNING id", 1)
		s.Prepared = prepared
		run(s)
		insertReturningFailure(t, p, b, b.DB, "INSERT INTO t VALUES(4,'bad') RETURNING v::INT", prepared, false, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`})
		run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "changed"}, {int64(2), "two"}, {int64(3), "d"}}))
	})
}
