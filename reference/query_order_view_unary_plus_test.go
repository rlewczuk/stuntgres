//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceQueryOrderViewUnaryPlusRegression(t *testing.T) {
	for _, key := range []string{"+1.5", "+1e0", "+2147483648"} {
		t.Run(key, func(t *testing.T) {
			p := newReferencePair(t)
			p.step(t, referenceExec("CREATE TABLE t(id INT)", 0))
			p.step(t, referenceExec("INSERT INTO t VALUES(1),(2),(2),(3)", 4))
			for _, backend := range []struct {
				name     string
				db       *sql.DB
				recorder *referenceRecorder
			}{{"memory", p.memory, p.memoryRecord}, {"postgres", p.postgres, p.postgresRecord}} {
				t.Run(backend.name, func(t *testing.T) {
					run := func(step referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, step) }
					run(referenceExec("CREATE VIEW constant_order AS SELECT id FROM t ORDER BY "+key, 0))
					run(referenceExec("CREATE VIEW later_order AS SELECT id FROM t ORDER BY "+key+",id DESC", 0))
					run(referenceExec("CREATE TABLE empty_t(id INT)", 0))
					run(referenceExec("CREATE VIEW empty_order AS SELECT id FROM empty_t ORDER BY "+key, 0))
					for _, name := range []string{"constant_order", "later_order", "empty_order"} {
						var definition string
						if err := backend.db.QueryRowContext(p.ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname='"+name+"'").Scan(&definition); err != nil {
							t.Fatal(err)
						}
						run(referenceExec("CREATE VIEW replay_"+name+" AS "+definition, 0))
					}
					for _, prefix := range []string{"", "replay_"} {
						step := referenceSelect("SELECT id FROM "+prefix+"constant_order", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(2)}, {int64(3)}})
						step.bag = true
						run(step)
						run(referencePrepared(step))
						ordered := referenceSelect("SELECT id FROM "+prefix+"later_order ORDER BY id DESC", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(2)}, {int64(2)}, {int64(1)}})
						run(ordered)
						run(referencePrepared(ordered))
						empty := referenceSelect("SELECT id FROM "+prefix+"empty_order", []string{"id"}, []string{"INT4"}, nil)
						run(empty)
						run(referencePrepared(empty))
					}
				})
			}
		})
	}
}
