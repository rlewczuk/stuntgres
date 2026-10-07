//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceQueryOrderViewRegression(t *testing.T) {
	p := newReferencePair(t)
	for _, step := range []referenceStep{
		referenceExec("CREATE TABLE t(id INT,a INT)", 0),
		referenceExec("INSERT INTO t VALUES(1,2),(2,1),(3,NULL)", 3),
		referenceExec("CREATE VIEW alias_order(k,id) AS SELECT a+0 AS x,id FROM t ORDER BY x DESC NULLS LAST,id", 0),
		referenceExec("CREATE VIEW hidden_order AS SELECT id FROM t ORDER BY COALESCE(a,0)+1 DESC NULLS LAST,id", 0),
		referenceExec("CREATE VIEW ordinal_order AS SELECT id,a FROM t ORDER BY 2 NULLS FIRST,1 DESC", 0),
		referenceExec("ALTER TABLE t RENAME COLUMN a TO renamed", 0),
	} {
		p.step(t, step)
	}
	for _, db := range []*sql.DB{p.memory, p.postgres} {
		for _, name := range []string{"alias_order", "hidden_order", "ordinal_order"} {
			var definition string
			if err := db.QueryRowContext(p.ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname='"+name+"'").Scan(&definition); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(p.ctx, "CREATE VIEW replay_"+name+" AS "+definition); err != nil {
				t.Fatal(name, definition, err)
			}
		}
	}
	for _, prefix := range []string{"", "replay_"} {
		for _, step := range []referenceStep{
			referenceSelect("SELECT k,id FROM "+prefix+"alias_order ORDER BY id", []string{"k", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(1)}, {int64(1), int64(2)}, {nil, int64(3)}}),
			referenceSelect("SELECT id FROM "+prefix+"hidden_order ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}),
			referenceSelect("SELECT id,a FROM "+prefix+"ordinal_order ORDER BY id", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(1)}, {int64(3), nil}}),
		} {
			p.step(t, step)
			p.step(t, referencePrepared(step))
		}
	}
	for _, step := range []referenceStep{
		referenceExec("CREATE SEQUENCE alias_sequence START WITH 1", 0),
		referenceExec("CREATE SEQUENCE hidden_sequence START WITH 1", 0),
		referenceExec("CREATE VIEW volatile_alias AS SELECT nextval('alias_sequence') AS n FROM t ORDER BY n DESC", 0),
		referenceExec("CREATE VIEW volatile_hidden AS SELECT id FROM t ORDER BY nextval('hidden_sequence') DESC", 0),
		referenceSelect("SELECT n FROM volatile_alias ORDER BY n DESC", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}, {int64(2)}, {int64(1)}}),
		referenceSelect("SELECT id FROM volatile_hidden ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}),
		referenceSelect("SELECT nextval('alias_sequence')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(4)}}),
		referenceSelect("SELECT nextval('hidden_sequence')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(4)}}),
	} {
		p.step(t, step)
	}
}
