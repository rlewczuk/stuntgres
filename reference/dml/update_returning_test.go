package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateReturningVersions(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					db := backend.DB
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, db, backend.Recorder, s) }
					result, err := db.Exec("CREATE TABLE t(id INT NOT NULL,a INT,b INT,v TEXT)")
					fixture.DeleteReferenceCount(t, result, err, 0)
					result, err = db.Exec("INSERT INTO t VALUES(1,10,20,'one'),(2,30,40,NULL),(2,30,40,NULL)")
					fixture.DeleteReferenceCount(t, result, err, 3)
					q := "UPDATE t SET a=b,b=a WHERE id=$1 RETURNING id,a,b,old.a,new.a,new.a-old.a AS delta,v"
					var stmt *sql.Stmt
					var recorded fixture.Executor = db
					if prepared {
						stmt, err = db.Prepare(q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						held, err := db.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer held.Close()
						recorded = fixture.HeldStatement{Executor: db, Statement: held, SQL: q}
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
						fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"id", "a", "b", "a", "a", "delta", "v"}, Types: []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "TEXT"}, Rows: want, Bag: true})
					}
					reset, resetErr := db.Exec("UPDATE t SET a=b,b=a WHERE id=2")
					fixture.DeleteReferenceCount(t, reset, resetErr, 2)
					for _, id := range []int{2, 99} {
						want := [][]any{}
						if id == 2 {
							want = [][]any{{int64(2), int64(40), int64(30), int64(30), int64(40), int64(10), nil}, {int64(2), int64(40), int64(30), int64(30), int64(40), int64(10), nil}}
						}
						s := fixture.Select(q, []string{"id", "a", "b", "a", "a", "delta", "v"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "TEXT"}, want, id)
						s.Bag = true
						fixture.RunStep(t, p.Ctx, recorded, backend.Recorder, s)
					}
					aliasQuery := "UPDATE t SET a=a+1 WHERE id=1 RETURNING WITH (OLD AS o,NEW AS n) o.a,n.a,t.a"
					rows, err := db.Query(aliasQuery)
					fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"a", "a", "a"}, Types: []string{"INT4", "INT4", "INT4"}, Rows: [][]any{{int64(10), int64(11), int64(11)}}, Bag: false})
					run(fixture.Exec("UPDATE t SET a=10 WHERE id=1", 1))
					run(fixture.Select(aliasQuery, []string{"a", "a", "a"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(10), int64(11), int64(11)}}))
					result, err = db.Exec("UPDATE t SET a=a RETURNING old.a,new.a")
					fixture.DeleteReferenceCount(t, result, err, 3)
					run(fixture.Exec("UPDATE t SET a=a RETURNING old.a,new.a", 3))
					final := fixture.Select("SELECT id,a,b,v FROM t", []string{"id", "a", "b", "v"}, []string{"INT4", "INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(11), int64(20), "one"}, {int64(2), int64(40), int64(30), nil}, {int64(2), int64(40), int64(30), nil}})
					final.Bag = true
					rows, err = db.Query(final.Query)
					fixture.DeleteReferenceConsume(t, backend.PG, rows, err, final)
					run(final)
				})
			}
		})
	}
}
