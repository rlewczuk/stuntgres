//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceQueryOrderViewOverrideRegression(t *testing.T) {
	p := newReferencePair(t)
	for _, step := range []referenceStep{
		referenceExec("CREATE TABLE t(id INT,b INT)", 0),
		referenceExec("INSERT INTO t VALUES(1,2),(2,1)", 2),
		referenceExec("CREATE SEQUENCE s START WITH 1", 0),
		referenceExec("CREATE VIEW collision(b,id) AS SELECT nextval('s') AS n,id FROM t ORDER BY b,id", 0),
	} {
		p.step(t, step)
	}
	for _, db := range []*sql.DB{p.memory, p.postgres} {
		var definition string
		if err := db.QueryRowContext(p.ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname='collision'").Scan(&definition); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(p.ctx, "CREATE VIEW replay AS "+definition); err != nil {
			t.Fatal(definition, err)
		}
	}
	for _, name := range []string{"collision", "replay"} {
		for _, prepared := range []bool{false, true} {
			p.step(t, referenceExec("ALTER SEQUENCE s RESTART WITH 1", 0))
			// The hidden physical b key admits id2 before id1 to volatile output
			// evaluation. Outer id ordering fixes the independently expected pairs.
			step := referenceSelect("SELECT b,id FROM "+name+" ORDER BY id", []string{"b", "id"}, []string{"INT8", "INT4"}, [][]any{{int64(2), int64(1)}, {int64(1), int64(2)}})
			step.prepared = prepared
			p.step(t, step)
			p.step(t, referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
		}
	}
}
