//go:build reference

package reference_test

import (
	"database/sql"
	"math"
	"testing"
)

func TestReferenceQueryOrderViewUsingOverrideRegression(t *testing.T) {
	p := newReferencePair(t)
	for _, step := range []referenceStep{
		referenceExec("CREATE TABLE a(id INT,k VARCHAR(2))", 0),
		referenceExec("CREATE TABLE b(k VARCHAR(3))", 0),
		referenceExec("CREATE SEQUENCE s START WITH 1", 0),
		referenceExec("CREATE VIEW collision(k,id) AS SELECT nextval('s') AS n,a.id FROM a JOIN b USING(k) ORDER BY k,a.id", 0),
		referenceExec("CREATE VIEW merged_usage AS SELECT k,a.id FROM a JOIN b USING(k) ORDER BY k,a.id", 0),
	} {
		p.step(t, step)
	}
	for _, db := range []*sql.DB{p.memory, p.postgres} {
		for _, name := range []string{"collision", "merged_usage"} {
			var definition string
			if err := db.QueryRowContext(p.ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname='"+name+"'").Scan(&definition); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(p.ctx, "CREATE VIEW replay_"+name+" AS "+definition); err != nil {
				t.Fatal(definition, err)
			}
		}
	}
	for stage := 0; stage < 3; stage++ {
		populated := stage == 2
		if stage == 1 {
			p.step(t, referenceExec("INSERT INTO a VALUES(1,'b'),(2,'a')", 2))
		}
		if populated {
			p.step(t, referenceExec("INSERT INTO b VALUES('a'),('b')", 2))
		}
		for _, name := range []string{"collision", "replay_collision"} {
			for _, prepared := range []bool{false, true} {
				p.step(t, referenceExec("ALTER SEQUENCE s RESTART WITH 1", 0))
				want := [][]any{{int64(2), int64(1)}, {int64(1), int64(2)}}
				next := int64(3)
				if !populated {
					want = nil
					next = 1
				}
				step := referenceSelect("SELECT k,id FROM "+name+" ORDER BY id", []string{"k", "id"}, []string{"INT8", "INT4"}, want)
				step.prepared = prepared
				p.step(t, step)
				p.step(t, referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{next}}))
			}
		}
		for _, prefix := range []string{"", "replay_"} {
			want := [][]any{{"b", int64(1)}, {"a", int64(2)}}
			if !populated {
				want = nil
			}
			step := referenceSelect("SELECT k,id FROM "+prefix+"merged_usage ORDER BY id", []string{"k", "id"}, []string{"VARCHAR", "INT4"}, want)
			p.step(t, step)
			p.step(t, referencePrepared(step))
			rows, err := p.memory.QueryContext(p.ctx, "SELECT k FROM "+prefix+"merged_usage")
			if err != nil {
				t.Fatal(err)
			}
			types, err := rows.ColumnTypes()
			if err != nil || len(types) != 1 {
				rows.Close()
				t.Fatal(types, err)
			}
			length, known := types[0].Length()
			rows.Close()
			if !known || length != math.MaxInt64 {
				t.Fatal("native lost merged VARCHAR usage", length, known)
			}
			// PG's actual stored output typmod independently specifies the same
			// unbounded merged usage; adapter-specific Length is not normalized.
			var typmod int64
			q := "SELECT att.atttypmod FROM pg_catalog.pg_attribute att JOIN pg_catalog.pg_class cls ON att.attrelid=cls.oid WHERE cls.relname='" + prefix + "merged_usage' AND att.attname='k'"
			if err := p.postgres.QueryRowContext(p.ctx, q).Scan(&typmod); err != nil || typmod != -1 {
				t.Fatal("PostgreSQL merged typmod", typmod, err)
			}
		}
	}
}
