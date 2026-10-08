package reference_test

import (
	"context"
	"testing"
)

func TestReferenceQueryAggregateBehaviorAndCallers(t *testing.T) {
	p := newReferenceScenario(t)
	for _, step := range []referenceStep{referenceExec("CREATE TABLE t(k TEXT,v INT)", 0), referenceExec("INSERT INTO t VALUES('a',1),('a',NULL),(NULL,2)", 3), referenceExec("CREATE TABLE empty(k TEXT,v INT)", 0), referenceExec("CREATE TABLE out(k TEXT,n BIGINT)", 0)} {
		p.step(t, step)
	}
	for _, step := range []referenceStep{
		referenceSelect("SELECT k FROM t GROUP BY k", []string{"k"}, []string{"TEXT"}, [][]any{{"a"}, {nil}}),
		referenceSelect("SELECT count(*),count(*) FROM t", []string{"count", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(3), int64(3)}}),
		referenceSelect("SELECT count(*) FROM t WHERE FALSE", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}),
		referenceSelect("SELECT count(*) FROM t WHERE NULL", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}),
		referenceSelect("SELECT count(*) FROM t WHERE v=NULL", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}),
		referenceSelect("SELECT k,count(*) FROM t GROUP BY k,k", []string{"k", "count"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {nil, int64(1)}}),
		referenceSelect("SELECT 7 FROM t GROUP BY k", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}}),
		referenceSelect("SELECT count(*)+1 FROM empty", []string{"?column?"}, []string{"INT8"}, [][]any{{int64(1)}}),
		referenceSelect("SELECT count(*) FROM empty HAVING count(*)=0", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}),
		referenceSelect("SELECT count(*) FROM empty HAVING count(*)>0", []string{"count"}, []string{"INT8"}, nil),
		referenceSelect("SELECT k,count(*) FROM t GROUP BY k HAVING k IS NULL", []string{"k", "count"}, []string{"TEXT", "INT8"}, [][]any{{nil, int64(1)}}),
		referenceSelect("SELECT k FROM t GROUP BY k HAVING FALSE", []string{"k"}, []string{"TEXT"}, nil),
		referenceSelect("SELECT k FROM t GROUP BY k HAVING NULL", []string{"k"}, []string{"TEXT"}, nil),
		referenceSelect("SELECT count(*) FROM t HAVING $1", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}, true),
		referenceSelect("SELECT count(*) FROM t HAVING $1", []string{"count"}, []string{"INT8"}, nil, nil),
		referenceSelect("SELECT v+1,count(*) FROM t GROUP BY v", []string{"?column?", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(2), int64(1)}, {nil, int64(1)}, {int64(3), int64(1)}}),
		referenceSelect("SELECT k,count(*) FROM t GROUP BY 1", []string{"k", "count"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {nil, int64(1)}}),
		referenceSelect("SELECT count(*) FROM t GROUP BY +1", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}),
		referenceSelect("SELECT count(*) FROM empty GROUP BY +1.5", []string{"count"}, []string{"INT8"}, nil),
		referenceSelect("SELECT k,count(*) FROM t GROUP BY (1)", []string{"k", "count"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {nil, int64(1)}}),
	} {
		step.bag = true
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	p.step(t, referenceExec("CREATE TABLE pairs(a TEXT,b TEXT)", 0))
	p.step(t, referenceExec("INSERT INTO pairs VALUES('a',NULL),('a',NULL),(NULL,'a'),(NULL,'a'),(NULL,NULL),('ab','c'),('a','bc')", 7))
	pair := referenceSelect("SELECT a,b,count(*) FROM pairs GROUP BY a,b", []string{"a", "b", "count"}, []string{"TEXT", "TEXT", "INT8"}, [][]any{{"a", nil, int64(2)}, {nil, "a", int64(2)}, {nil, nil, int64(1)}, {"ab", "c", int64(1)}, {"a", "bc", int64(1)}})
	pair.bag = true
	p.step(t, pair)
	p.step(t, referencePrepared(pair))
	for _, prepared := range []bool{false, true} {
		step := referenceExec("INSERT INTO out SELECT k,count(*) FROM t GROUP BY k", 2)
		step.prepared = prepared
		p.step(t, step)
	}
	out := referenceSelect("SELECT k,n FROM out", []string{"k", "n"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {nil, int64(1)}, {"a", int64(2)}, {nil, int64(1)}})
	out.bag = true
	p.step(t, out)
	p.step(t, referenceExec("CREATE TABLE numbers(n SMALLINT)", 0))
	for _, step := range []referenceStep{referenceExec("INSERT INTO numbers SELECT count(*) FROM empty", 1), referenceExec("INSERT INTO numbers SELECT count(*) FROM empty GROUP BY k", 0), referenceExec("INSERT INTO numbers SELECT count(*) FROM t HAVING count(*)>5", 0)} {
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	p.step(t, referenceSelect("SELECT n FROM numbers", []string{"n"}, []string{"INT2"}, [][]any{{int64(0)}, {int64(0)}}))
	p.step(t, referenceExec("CREATE TABLE zero(v INT)", 0))
	p.step(t, referenceExec("INSERT INTO zero VALUES(NULL),(NULL)", 2))
	p.step(t, referenceExec("ALTER TABLE zero DROP COLUMN v", 0))
	zero := referenceSelect("SELECT count(*) FROM zero", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}})
	p.step(t, zero)
	p.step(t, referencePrepared(zero))
}

func TestReferenceQueryAggregatePreparedFreshnessAndHeldRows(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(k TEXT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES('a'),('a'),(NULL)", 3))
	for _, backend := range p.backends() {
		db := backend.db
		stmt, err := db.PrepareContext(p.ctx, "SELECT count(*) FROM t")
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer stmt.Close()
			held, err := stmt.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder))
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			backend.recorder.mu.Lock()
			heldRecord := backend.recorder.operations[len(backend.recorder.operations)-1]
			backend.recorder.mu.Unlock()
			referenceRunStep(t, p.ctx, db, backend.recorder, referenceExec("INSERT INTO t VALUES('b')", 1))
			queryCountHeldRows(t, held, 3)
			backend.recorder.mu.Lock()
			recorded := heldRecord.Closed && heldRecord.CloseErr == nil && len(heldRecord.Columns) == 1 && heldRecord.Columns[0] == "count" && len(heldRecord.Rows) == 1 && len(heldRecord.Rows[0]) == 1 && heldRecord.Rows[0][0] == int64(3)
			backend.recorder.mu.Unlock()
			if !recorded {
				t.Fatal("held aggregate consumed output/closure not recorded")
			}
			fresh, err := stmt.QueryContext(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			queryCountHeldRows(t, fresh, 4)
			referenceRunStep(t, p.ctx, referenceHeldStatement{db, stmt, "SELECT count(*) FROM t"}, backend.recorder, referenceSelect("SELECT count(*) FROM t", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}))
		}()
	}
}
