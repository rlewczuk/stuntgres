package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func updateRowBackends(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { body(t, p, backend) })
	}
}

func updateRowSeed(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE t(id INT,a INT,b INT)", 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO t VALUES(1,10,20),(2,30,40)", 2))
}

func updateRowSnapshot(want [][]any) fixture.Step {
	return fixture.Select("SELECT id,a,b FROM t ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "INT4", "INT4"}, want)
}

func TestReferenceUpdateRowConstructors(t *testing.T) {
	updateRowBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		updateRowSeed(t, p, b)
		run(fixture.Exec("UPDATE t SET (a,b)=(b,a)", 2))
		run(updateRowSnapshot([][]any{{int64(1), int64(20), int64(10)}, {int64(2), int64(40), int64(30)}}))
		run(fixture.Prepared(fixture.Exec("UPDATE t SET (a,b)=ROW(b,a)", 2)))
		run(updateRowSnapshot([][]any{{int64(1), int64(10), int64(20)}, {int64(2), int64(30), int64(40)}}))
		run(fixture.Exec("UPDATE t SET (a,b)=(b,a),id=id+10", 2))
		run(updateRowSnapshot([][]any{{int64(11), int64(20), int64(10)}, {int64(12), int64(40), int64(30)}}))
		run(fixture.Exec("UPDATE t SET (a,b)=ROW(a,b)", 2))
		run(fixture.Exec("UPDATE t SET (a,b)=ROW(1,2) WHERE FALSE", 0))
		run(fixture.Exec("CREATE TABLE four(a INT,b INT,c INT,d INT)", 0))
		run(fixture.Exec("INSERT INTO four VALUES(1,2,3,4)", 1))
		run(fixture.Exec("UPDATE four SET (a,b)=(c,d),(c,d)=(a,b)", 1))
		run(fixture.Select("SELECT a,b,c,d FROM four", []string{"a", "b", "c", "d"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(3), int64(4), int64(1), int64(2)}}))
		run(fixture.Exec("UPDATE t SET (a)=ROW(7) WHERE id=11", 1))
		run(fixture.Exec("CREATE TABLE pair(x INT,y INT)", 0))
		run(fixture.Exec("INSERT INTO pair VALUES(5,6)", 1))
		run(fixture.Exec("UPDATE t SET (a,b)=ROW(pair.*) FROM pair WHERE id=11", 1))
		run(updateRowSnapshot([][]any{{int64(11), int64(5), int64(6)}, {int64(12), int64(40), int64(30)}}))
	})
}

func TestReferenceUpdateRowSubqueries(t *testing.T) {
	updateRowBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		updateRowSeed(t, p, b)
		run(fixture.Exec("CREATE TABLE s(id INT,x INT,y INT)", 0))
		run(fixture.Exec("INSERT INTO s VALUES(1,100,200),(2,300,400)", 2))
		q := "UPDATE t SET (a,b)=(SELECT x,y FROM s WHERE s.id=t.id)"
		run(fixture.Exec(q, 2))
		run(updateRowSnapshot([][]any{{int64(1), int64(100), int64(200)}, {int64(2), int64(300), int64(400)}}))
		run(fixture.Exec("DELETE FROM s WHERE id=1", 1))
		run(fixture.Prepared(fixture.Exec(q+" WHERE t.id=1", 1)))
		run(updateRowSnapshot([][]any{{int64(1), nil, nil}, {int64(2), int64(300), int64(400)}}))
		run(fixture.Exec("INSERT INTO s VALUES(1,NULL,9)", 1))
		run(fixture.Exec(q+" WHERE t.id=1", 1))
		run(updateRowSnapshot([][]any{{int64(1), nil, int64(9)}, {int64(2), int64(300), int64(400)}}))
		run(fixture.Exec("CREATE TABLE g(a INT,b INT,c INT,d INT)", 0))
		run(fixture.Exec("INSERT INTO g VALUES(1,2,3,4)", 1))
		run(fixture.Exec("UPDATE g SET (a,b)=(SELECT c,d),(c,d)=(SELECT a,b)", 1))
		run(fixture.Select("SELECT a,b,c,d FROM g", []string{"a", "b", "c", "d"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(3), int64(4), int64(1), int64(2)}}))
	})
}

func TestReferenceUpdateRowTargets(t *testing.T) {
	updateRowBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TYPE pair AS (x INT,y INT)", 0))
		run(fixture.Exec("CREATE TABLE paths(p pair,v INT[])", 0))
		run(fixture.Exec("INSERT INTO paths VALUES(ROW(1,2),ARRAY[3,4])", 1))
		run(fixture.Exec("UPDATE paths SET (p.x,p.y)=((p).y,(p).x),(v[1],v[2])=(v[2],v[1])", 1))
		run(fixture.Select("SELECT (p).x,(p).y,v[1],v[2] FROM paths", []string{"x", "y", "v", "v"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(1), int64(4), int64(3)}}))
	})
}

func TestReferenceUpdateRowDemand(t *testing.T) {
	updateRowBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		updateRowSeed(t, p, b)
		run(fixture.Exec("CREATE SEQUENCE draw", 0))
		q := "UPDATE t SET (a,b)=(WITH v AS MATERIALIZED (SELECT nextval('draw') + t.id*0 AS x) SELECT x,x+100 FROM v) WHERE id>$1"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		// Preparing must not draw; an unmatched execution must not draw either.
		result, err := stmt.ExecContext(p.Ctx, 2)
		if err != nil {
			t.Fatal(err)
		}
		fixture.LockAffected(t, result, 0)
		run(fixture.Select("SELECT last_value,is_called FROM draw", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
		for _, last := range []int64{2, 4} {
			result, err = stmt.ExecContext(p.Ctx, 0)
			if err != nil {
				t.Fatal(err)
			}
			fixture.LockAffected(t, result, 2)
			run(fixture.Select("SELECT b-a FROM t ORDER BY id", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(100)}, {int64(100)}}))
			run(fixture.Select("SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{last - 1}, {last}}))
			run(fixture.Select("SELECT last_value FROM draw", []string{"last_value"}, []string{"INT8"}, [][]any{{last}}))
		}
		run(fixture.Exec("UPDATE t SET (a,b)=ROW(nextval('draw'),nextval('draw')) WHERE id=1", 1))
		run(fixture.Select("SELECT a+b,abs(a-b) FROM t WHERE id=1", []string{"?column?", "abs"}, []string{"INT4", "INT4"}, [][]any{{int64(11), int64(1)}}))
		run(fixture.Select("SELECT last_value FROM draw", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(6)}}))
	})
}
