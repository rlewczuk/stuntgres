package reference_test

import "testing"

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
					run(referenceExec("CREATE TABLE t(id INT NOT NULL,a INT,b INT,v TEXT)", 0))
					run(referenceExec("INSERT INTO t VALUES(1,10,20,'one'),(2,30,40,NULL),(2,30,40,NULL)", 3))
					q := "UPDATE t SET a=b,b=a WHERE id=$1 RETURNING id,a,b,old.a,new.a,new.a-old.a AS delta,v"
					var x referenceExecutor = db
					if prepared {
						held, err := db.PrepareContext(p.ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer held.Close()
						x = referenceHeldStatement{referenceExecutor: db, statement: held, query: q}
					}
					for _, id := range []int{2, 99} {
						want := [][]any{}
						if id == 2 {
							want = [][]any{{int64(2), int64(40), int64(30), int64(30), int64(40), int64(10), nil}, {int64(2), int64(40), int64(30), int64(30), int64(40), int64(10), nil}}
						}
						s := referenceSelect(q, []string{"id", "a", "b", "a", "a", "delta", "v"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "TEXT"}, want, id)
						s.bag = true
						referenceRunStep(t, p.ctx, x, backend.recorder, s)
					}
					run(referenceSelect("UPDATE t SET a=a+1 WHERE id=1 RETURNING WITH (OLD AS o,NEW AS n) o.a,n.a,t.a", []string{"a", "a", "a"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(10), int64(11), int64(11)}}))
					run(referenceExec("UPDATE t SET a=a RETURNING old.a,new.a", 3))
					s := referenceSelect("SELECT id,a,b,v FROM t", []string{"id", "a", "b", "v"}, []string{"INT4", "INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(11), int64(20), "one"}, {int64(2), int64(40), int64(30), nil}, {int64(2), int64(40), int64(30), nil}})
					s.bag = true
					run(s)
				})
			}
		})
	}
}
