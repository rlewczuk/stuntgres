//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceQueryOrderViewUsingAliasNamespaces(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"reserved", "SELECT nextval('s') AS n,__view_using_0.id FROM a AS __view_using_0 JOIN b AS __view_using_0_1 USING(k,j) ORDER BY k,j,__view_using_0.id"},
		{"explicit", "SELECT nextval('s') AS n,a.id FROM a JOIN b USING(k,j) AS merged ORDER BY merged.k,merged.j,a.id"},
		{"whole_join", "SELECT nextval('s') AS n,q.id FROM (a JOIN b USING(k,j)) AS q(k,j,id) ORDER BY q.k,q.j,q.id"},
		{"nested", "SELECT nextval('s') AS n,a.id FROM (a JOIN b USING(k,j)) JOIN c USING(k,j) ORDER BY k,j,a.id"},
		{"natural", "SELECT nextval('s') AS n,a.id FROM a NATURAL JOIN b ORDER BY k,j,a.id"},
		{"mixed_slots", "SELECT nextval('s') AS n,a.id FROM a JOIN b USING(k,j) ORDER BY k,j,a.id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newReferencePair(t)
			jWidth := "3"
			if tc.name == "mixed_slots" {
				jWidth = "2"
			}
			for _, step := range []referenceStep{
				referenceExec("CREATE TABLE a(id INT,k VARCHAR(2),j VARCHAR(2))", 0), referenceExec("CREATE TABLE b(k VARCHAR(3),j VARCHAR("+jWidth+"))", 0), referenceExec("CREATE TABLE c(k VARCHAR(4),j VARCHAR(4))", 0),
				referenceExec("INSERT INTO a VALUES(1,'b','z'),(2,'a','y')", 2), referenceExec("INSERT INTO b VALUES('a','y'),('b','z')", 2), referenceExec("INSERT INTO c VALUES('a','y'),('b','z')", 2),
				referenceExec("CREATE SEQUENCE s START WITH 1", 0), referenceExec("CREATE VIEW collision(k,id) AS "+tc.body, 0),
			} {
				p.step(t, step)
			}
			for _, db := range []*sql.DB{p.memory, p.postgres} {
				var definition string
				definitionSource := db
				if tc.name == "nested" {
					// Pinned PostgreSQL18.0's own pg_views definition emits
					// bare ORDER k here, retargeting the hidden merged key to
					// the overridden volatile output. Its original view has
					// the independently asserted pairs below; its own replay
					// does not. Test the selected native serializer instead:
					// execute the same unmodified native definition on both
					// engines, preserving exact rows/types/effects assertions.
					definitionSource = p.memory
				}
				if err := definitionSource.QueryRowContext(p.ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname='collision'").Scan(&definition); err != nil {
					t.Fatal(err)
				}
				if _, err := db.ExecContext(p.ctx, "CREATE VIEW replay AS "+definition); err != nil {
					t.Fatal(definition, err)
				}
			}
			for _, name := range []string{"collision", "replay"} {
				for _, prepared := range []bool{false, true} {
					p.step(t, referenceExec("ALTER SEQUENCE s RESTART WITH 1", 0))
					step := referenceSelect("SELECT k,id FROM "+name+" ORDER BY id", []string{"k", "id"}, []string{"INT8", "INT4"}, [][]any{{int64(2), int64(1)}, {int64(1), int64(2)}})
					step.prepared = prepared
					p.step(t, step)
					p.step(t, referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
				}
			}
		})
	}
}
