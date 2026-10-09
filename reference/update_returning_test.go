package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceUpdateReturningVersions(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					db := backend.db
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, db, backend.recorder, s) }
					result, err := db.Exec("CREATE TABLE t(id INT NOT NULL,a INT,b INT,v TEXT)")
					deleteReferenceCount(t, result, err, 0)
					result, err = db.Exec("INSERT INTO t VALUES(1,10,20,'one'),(2,30,40,NULL),(2,30,40,NULL)")
					deleteReferenceCount(t, result, err, 3)
					q := "UPDATE t SET a=b,b=a WHERE id=$1 RETURNING id,a,b,old.a,new.a,new.a-old.a AS delta,v"
					var stmt *sql.Stmt
					var recorded referenceExecutor = db
					if prepared {
						stmt, err = db.Prepare(q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						held, err := db.PrepareContext(p.ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer held.Close()
						recorded = referenceHeldStatement{referenceExecutor: db, statement: held, query: q}
					}
					for _, id := range []int{2, 99} {
						var rows *sql.Rows
						if stmt != nil {
							rows, err = stmt.Query(id)
						} else {
							rows, err = db.Query(q, id)
						}
						want := [][]any{}
						if id == 2 {
							want = [][]any{{int64(2), int64(40), int64(30), int64(30), int64(40), int64(10), nil}, {int64(2), int64(40), int64(30), int64(30), int64(40), int64(10), nil}}
						}
						deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"id", "a", "b", "a", "a", "delta", "v"}, types: []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "TEXT"}, rows: want, bag: true})
					}
					reset, resetErr := db.Exec("UPDATE t SET a=b,b=a WHERE id=2")
					deleteReferenceCount(t, reset, resetErr, 2)
					for _, id := range []int{2, 99} {
						want := [][]any{}
						if id == 2 {
							want = [][]any{{int64(2), int64(40), int64(30), int64(30), int64(40), int64(10), nil}, {int64(2), int64(40), int64(30), int64(30), int64(40), int64(10), nil}}
						}
						s := referenceSelect(q, []string{"id", "a", "b", "a", "a", "delta", "v"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "TEXT"}, want, id)
						s.bag = true
						referenceRunStep(t, p.ctx, recorded, backend.recorder, s)
					}
					aliasQuery := "UPDATE t SET a=a+1 WHERE id=1 RETURNING WITH (OLD AS o,NEW AS n) o.a,n.a,t.a"
					rows, err := db.Query(aliasQuery)
					deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"a", "a", "a"}, types: []string{"INT4", "INT4", "INT4"}, rows: [][]any{{int64(10), int64(11), int64(11)}}, bag: false})
					run(referenceExec("UPDATE t SET a=10 WHERE id=1", 1))
					run(referenceSelect(aliasQuery, []string{"a", "a", "a"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(10), int64(11), int64(11)}}))
					result, err = db.Exec("UPDATE t SET a=a RETURNING old.a,new.a")
					deleteReferenceCount(t, result, err, 3)
					run(referenceExec("UPDATE t SET a=a RETURNING old.a,new.a", 3))
					final := referenceSelect("SELECT id,a,b,v FROM t", []string{"id", "a", "b", "v"}, []string{"INT4", "INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(11), int64(20), "one"}, {int64(2), int64(40), int64(30), nil}, {int64(2), int64(40), int64(30), nil}})
					final.bag = true
					rows, err = db.Query(final.query)
					deleteReferenceConsume(t, backend.pg, rows, err, final)
					run(final)
				})
			}
		})
	}
}
