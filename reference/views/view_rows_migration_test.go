package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewRows(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, s := range []fixture.Step{
				fixture.Exec("CREATE TABLE t(id INT,txt TEXT,visible BOOL)", 0), fixture.Exec("INSERT INTO t VALUES(1,'a',true),(2,'b',false),(3,NULL,true),(3,NULL,true)", 4), fixture.Exec("CREATE VIEW v(k,label) AS SELECT id,txt FROM t WHERE visible", 0), fixture.Exec("CREATE VIEW w AS SELECT k,label FROM v WHERE k>1", 0), fixture.Exec("CREATE VIEW z AS SELECT FROM t", 0),
				fixture.Select("SELECT k,label FROM v ORDER BY k", []string{"k", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(3), nil}, {int64(3), nil}}),
				fixture.Select("SELECT count(*) FROM w", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}), fixture.Select("SELECT count(*) FROM z", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}),
				fixture.Prepared(fixture.Select("SELECT FROM z", []string{}, []string{}, [][]any{{}, {}, {}, {}})), fixture.Exec("CREATE VIEW prefix(a) AS SELECT id,txt FROM t", 0), fixture.Exec("CREATE VIEW duplicate(a,b) AS SELECT id,id FROM t", 0),
				fixture.Prepared(fixture.Select("SELECT k,label FROM w WHERE k=$1 ORDER BY k", []string{"k", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), nil}, {int64(3), nil}}, 3)),
			} {
				run(s)
			}
			held := fixture.ViewHold(t, p, b, "SELECT k,label FROM w WHERE k=$1")
			for _, arg := range []any{3, nil} {
				want := [][]any{{int64(3), nil}, {int64(3), nil}}
				if arg == nil {
					want = nil
				}
				s := fixture.Select(held.SQL, []string{"k", "label"}, []string{"INT4", "TEXT"}, want, arg)
				s.Bag = true
				s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
					var k int64
					var label sql.NullString
					if err := rows.Scan(&k, &label); err != nil || k != 3 || label.Valid {
						t.Fatal(k, label, err)
					}
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
			run(fixture.Exec("INSERT INTO t VALUES(4,'d',true)", 1))
			run(fixture.Select("SELECT count(*) FROM v", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}))
		})
	}
}

func TestReferenceViewBarrier(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE b(txt TEXT,visible BOOL)", 0), fixture.Exec("INSERT INTO b VALUES('1',true),('oops',false)", 2), fixture.Exec("CREATE VIEW safe WITH(security_barrier=true) AS SELECT txt FROM b WHERE visible", 0), fixture.Exec("CREATE VIEW nested AS SELECT txt FROM safe", 0)} {
				run(s)
			}
			for _, name := range []string{"safe", "nested"} {
				run(fixture.Select("SELECT CAST(txt AS INT) FROM "+name+" WHERE CAST(txt AS INT)=1", []string{"txt"}, []string{"INT4"}, [][]any{{int64(1)}}))
			}
		})
	}
}
