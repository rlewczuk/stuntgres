package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceViewRows(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			for _, s := range []referenceStep{
				referenceExec("CREATE TABLE t(id INT,txt TEXT,visible BOOL)", 0), referenceExec("INSERT INTO t VALUES(1,'a',true),(2,'b',false),(3,NULL,true),(3,NULL,true)", 4), referenceExec("CREATE VIEW v(k,label) AS SELECT id,txt FROM t WHERE visible", 0), referenceExec("CREATE VIEW w AS SELECT k,label FROM v WHERE k>1", 0), referenceExec("CREATE VIEW z AS SELECT FROM t", 0),
				referenceSelect("SELECT k,label FROM v ORDER BY k", []string{"k", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(3), nil}, {int64(3), nil}}),
				referenceSelect("SELECT count(*) FROM w", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}), referenceSelect("SELECT count(*) FROM z", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}),
				referencePrepared(referenceSelect("SELECT FROM z", []string{}, []string{}, [][]any{{}, {}, {}, {}})), referenceExec("CREATE VIEW prefix(a) AS SELECT id,txt FROM t", 0), referenceExec("CREATE VIEW duplicate(a,b) AS SELECT id,id FROM t", 0),
				referencePrepared(referenceSelect("SELECT k,label FROM w WHERE k=$1 ORDER BY k", []string{"k", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), nil}, {int64(3), nil}}, 3)),
			} {
				run(s)
			}
			held := referenceViewHold(t, p, b, "SELECT k,label FROM w WHERE k=$1")
			for _, arg := range []any{3, nil} {
				want := [][]any{{int64(3), nil}, {int64(3), nil}}
				if arg == nil {
					want = nil
				}
				s := referenceSelect(held.query, []string{"k", "label"}, []string{"INT4", "TEXT"}, want, arg)
				s.bag = true
				s.checkScan = func(t *testing.T, rows *sql.Rows, _ int) {
					var k int64
					var label sql.NullString
					if err := rows.Scan(&k, &label); err != nil || k != 3 || label.Valid {
						t.Fatal(k, label, err)
					}
				}
				referenceRunStep(t, p.ctx, held, b.recorder, s)
			}
			run(referenceExec("INSERT INTO t VALUES(4,'d',true)", 1))
			run(referenceSelect("SELECT count(*) FROM v", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}))
		})
	}
}

func TestReferenceViewBarrier(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			for _, s := range []referenceStep{referenceExec("CREATE TABLE b(txt TEXT,visible BOOL)", 0), referenceExec("INSERT INTO b VALUES('1',true),('oops',false)", 2), referenceExec("CREATE VIEW safe WITH(security_barrier=true) AS SELECT txt FROM b WHERE visible", 0), referenceExec("CREATE VIEW nested AS SELECT txt FROM safe", 0)} {
				run(s)
			}
			for _, name := range []string{"safe", "nested"} {
				run(referenceSelect("SELECT CAST(txt AS INT) FROM "+name+" WHERE CAST(txt AS INT)=1", []string{"txt"}, []string{"INT4"}, [][]any{{int64(1)}}))
			}
		})
	}
}
