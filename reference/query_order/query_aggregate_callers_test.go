package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryAggregateBehaviorAndCallers(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, step := range []fixture.Step{fixture.Exec("CREATE TABLE t(k TEXT,v INT)", 0), fixture.Exec("INSERT INTO t VALUES('a',1),('a',NULL),(NULL,2)", 3), fixture.Exec("CREATE TABLE empty(k TEXT,v INT)", 0), fixture.Exec("CREATE TABLE out(k TEXT,n BIGINT)", 0)} {
		p.Step(t, step)
	}
	for _, step := range []fixture.Step{
		fixture.Select("SELECT k FROM t GROUP BY k", []string{"k"}, []string{"TEXT"}, [][]any{{"a"}, {nil}}),
		fixture.Select("SELECT count(*),count(*) FROM t", []string{"count", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(3), int64(3)}}),
		fixture.Select("SELECT count(*) FROM t WHERE FALSE", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}),
		fixture.Select("SELECT count(*) FROM t WHERE NULL", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}),
		fixture.Select("SELECT count(*) FROM t WHERE v=NULL", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}),
		fixture.Select("SELECT k,count(*) FROM t GROUP BY k,k", []string{"k", "count"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {nil, int64(1)}}),
		fixture.Select("SELECT 7 FROM t GROUP BY k", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}}),
		fixture.Select("SELECT count(*)+1 FROM empty", []string{"?column?"}, []string{"INT8"}, [][]any{{int64(1)}}),
		fixture.Select("SELECT count(*) FROM empty HAVING count(*)=0", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}),
		fixture.Select("SELECT count(*) FROM empty HAVING count(*)>0", []string{"count"}, []string{"INT8"}, nil),
		fixture.Select("SELECT k,count(*) FROM t GROUP BY k HAVING k IS NULL", []string{"k", "count"}, []string{"TEXT", "INT8"}, [][]any{{nil, int64(1)}}),
		fixture.Select("SELECT k FROM t GROUP BY k HAVING FALSE", []string{"k"}, []string{"TEXT"}, nil),
		fixture.Select("SELECT k FROM t GROUP BY k HAVING NULL", []string{"k"}, []string{"TEXT"}, nil),
		fixture.Select("SELECT count(*) FROM t HAVING $1", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}, true),
		fixture.Select("SELECT count(*) FROM t HAVING $1", []string{"count"}, []string{"INT8"}, nil, nil),
		fixture.Select("SELECT v+1,count(*) FROM t GROUP BY v", []string{"?column?", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(2), int64(1)}, {nil, int64(1)}, {int64(3), int64(1)}}),
		fixture.Select("SELECT k,count(*) FROM t GROUP BY 1", []string{"k", "count"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {nil, int64(1)}}),
		fixture.Select("SELECT count(*) FROM t GROUP BY +1", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}),
		fixture.Select("SELECT count(*) FROM empty GROUP BY +1.5", []string{"count"}, []string{"INT8"}, nil),
		fixture.Select("SELECT k,count(*) FROM t GROUP BY (1)", []string{"k", "count"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {nil, int64(1)}}),
	} {
		step.Bag = true
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
	p.Step(t, fixture.Exec("CREATE TABLE pairs(a TEXT,b TEXT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO pairs VALUES('a',NULL),('a',NULL),(NULL,'a'),(NULL,'a'),(NULL,NULL),('ab','c'),('a','bc')", 7))
	pair := fixture.Select("SELECT a,b,count(*) FROM pairs GROUP BY a,b", []string{"a", "b", "count"}, []string{"TEXT", "TEXT", "INT8"}, [][]any{{"a", nil, int64(2)}, {nil, "a", int64(2)}, {nil, nil, int64(1)}, {"ab", "c", int64(1)}, {"a", "bc", int64(1)}})
	pair.Bag = true
	p.Step(t, pair)
	p.Step(t, fixture.Prepared(pair))
	for _, prepared := range []bool{false, true} {
		step := fixture.Exec("INSERT INTO out SELECT k,count(*) FROM t GROUP BY k", 2)
		step.Prepared = prepared
		p.Step(t, step)
	}
	out := fixture.Select("SELECT k,n FROM out", []string{"k", "n"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {nil, int64(1)}, {"a", int64(2)}, {nil, int64(1)}})
	out.Bag = true
	p.Step(t, out)
	p.Step(t, fixture.Exec("CREATE TABLE numbers(n SMALLINT)", 0))
	for _, step := range []fixture.Step{fixture.Exec("INSERT INTO numbers SELECT count(*) FROM empty", 1), fixture.Exec("INSERT INTO numbers SELECT count(*) FROM empty GROUP BY k", 0), fixture.Exec("INSERT INTO numbers SELECT count(*) FROM t HAVING count(*)>5", 0)} {
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
	p.Step(t, fixture.Select("SELECT n FROM numbers", []string{"n"}, []string{"INT2"}, [][]any{{int64(0)}, {int64(0)}}))
	p.Step(t, fixture.Exec("CREATE TABLE zero(v INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO zero VALUES(NULL),(NULL)", 2))
	p.Step(t, fixture.Exec("ALTER TABLE zero DROP COLUMN v", 0))
	zero := fixture.Select("SELECT count(*) FROM zero", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}})
	p.Step(t, zero)
	p.Step(t, fixture.Prepared(zero))
}

func TestReferenceQueryAggregatePreparedFreshnessAndHeldRows(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(k TEXT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES('a'),('a'),(NULL)", 3))
	for _, backend := range p.Backends() {
		db := backend.DB
		stmt, err := db.PrepareContext(p.Ctx, "SELECT count(*) FROM t")
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer stmt.Close()
			held, err := stmt.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder))
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			backend.Recorder.Mu.Lock()
			heldRecord := backend.Recorder.Operations[len(backend.Recorder.Operations)-1]
			backend.Recorder.Mu.Unlock()
			fixture.RunStep(t, p.Ctx, db, backend.Recorder, fixture.Exec("INSERT INTO t VALUES('b')", 1))
			queryCountHeldRows(t, held, 3)
			backend.Recorder.Mu.Lock()
			recorded := heldRecord.Closed && heldRecord.CloseErr == nil && len(heldRecord.Columns) == 1 && heldRecord.Columns[0] == "count" && len(heldRecord.Rows) == 1 && len(heldRecord.Rows[0]) == 1 && heldRecord.Rows[0][0] == int64(3)
			backend.Recorder.Mu.Unlock()
			if !recorded {
				t.Fatal("held aggregate consumed output/closure not recorded")
			}
			fresh, err := stmt.QueryContext(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			queryCountHeldRows(t, fresh, 4)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{db, stmt, "SELECT count(*) FROM t"}, backend.Recorder, fixture.Select("SELECT count(*) FROM t", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}))
		}()
	}
}
