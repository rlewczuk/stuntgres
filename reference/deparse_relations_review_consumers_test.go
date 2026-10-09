package reference_test

import (
	"database/sql"
	"fmt"
	"math"
	"testing"
)

func TestReferenceDeparseRelationsClauseConsumers(t *testing.T) {
	deparseRelationsReviewScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		deparseRelationsSeed(t, p, b, c)
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("INSERT INTO t VALUES(4,'d',true)", 1))
		run(referenceExec("CREATE TABLE l(id INT,txt TEXT)", 0))
		run(referenceExec("INSERT INTO l VALUES(1,'a'),(2,'b'),(3,NULL)", 3))
		run(referenceExec("CREATE TABLE lookup(id INT,mark TEXT)", 0))
		run(referenceExec("INSERT INTO lookup VALUES(1,'u'),(1,'v'),(4,'orphan')", 3))
		for _, tc := range []struct {
			name, body     string
			columns, types []string
			rows           [][]any
			bag            bool
		}{
			{"left_export", "SELECT l.id,l.txt,lookup.mark FROM l LEFT JOIN lookup ON l.id=lookup.id", []string{"id", "txt", "mark"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "a", "u"}, {int64(1), "a", "v"}, {int64(2), "b", nil}, {int64(3), nil, nil}}, true},
			{"full_export", "SELECT l.id,l.txt,lookup.mark FROM l FULL JOIN lookup ON l.id=lookup.id", []string{"id", "txt", "mark"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "a", "u"}, {int64(1), "a", "v"}, {int64(2), "b", nil}, {int64(3), nil, nil}, {nil, nil, "orphan"}}, true},
			{"having_export", "SELECT id,count(*) AS n FROM t WHERE visible GROUP BY id HAVING count(*)>1 ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(3), int64(2)}}, false},
			{"having_empty", "SELECT id,count(*) AS n FROM t WHERE visible GROUP BY id HAVING count(*)>9 ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT8"}, nil, false},
			{"limit_export", "SELECT id,txt FROM t WHERE visible ORDER BY id DESC LIMIT 2 OFFSET 1", []string{"id", "txt"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), nil}, {int64(3), nil}}, false},
			{"limit_empty", "SELECT id,txt FROM t WHERE visible ORDER BY id DESC LIMIT 2 OFFSET 99", []string{"id", "txt"}, []string{"INT4", "TEXT"}, nil, false},
		} {
			run(referenceExec("CREATE VIEW "+tc.name+" AS "+tc.body, 0))
			for i, flag := range []string{"false", "true", "20"} {
				d := deparseRelationsRead(t, p, b, c, "SELECT pg_get_viewdef('"+tc.name+"'::regclass,"+flag+") AS definition")
				copyName := fmt.Sprintf("%s_copy_%d", tc.name, i)
				run(referenceExec("CREATE VIEW "+copyName+" AS "+d, 0))
				for _, name := range []string{tc.name, copyName} {
					for _, prepared := range []bool{false, true} {
						s := referenceSelect("SELECT * FROM "+name, tc.columns, tc.types, tc.rows)
						s.bag = tc.bag
						s.prepared = prepared
						run(s)
					}
				}
			}
		}
		run(referenceExec("DELETE FROM lookup", 3))
		for _, prefix := range []string{"left_export", "full_export"} {
			names := []string{prefix, prefix + "_copy_0", prefix + "_copy_1", prefix + "_copy_2"}
			for _, name := range names {
				for _, prepared := range []bool{false, true} {
					s := referenceSelect("SELECT * FROM "+name, []string{"id", "txt", "mark"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "a", nil}, {int64(2), "b", nil}, {int64(3), nil, nil}})
					s.bag = true
					s.prepared = prepared
					run(s)
				}
			}
		}
	})
}

func TestReferenceDeparseRelationsUsingOverrideConsumers(t *testing.T) {
	deparseRelationsReviewScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		for _, s := range []referenceStep{
			referenceExec("CREATE TABLE a(id INT,k VARCHAR(2),j VARCHAR(2))", 0),
			referenceExec("CREATE TABLE b(k VARCHAR(3),j VARCHAR(3))", 0),
			referenceExec("CREATE TABLE c(k VARCHAR(4),j VARCHAR(4))", 0),
			referenceExec("INSERT INTO a VALUES(1,'b','z'),(2,'a','y')", 2),
			referenceExec("INSERT INTO b VALUES('a','y'),('b','z')", 2),
			referenceExec("INSERT INTO c VALUES('a','y'),('b','z')", 2),
			referenceExec("CREATE SEQUENCE s START WITH 1", 0),
			referenceExec("CREATE VIEW collision(k,id) AS SELECT nextval('s') AS n,a.id FROM (a JOIN b USING(k,j)) JOIN c USING(k,j) ORDER BY k,j,a.id", 0),
			referenceExec("CREATE VIEW merged_usage AS SELECT k,__view_using_0.id FROM a AS __view_using_0 JOIN b USING(k,j) ORDER BY k,j,__view_using_0.id", 0),
		} {
			run(s)
		}
		original := [][]any{{int64(2), int64(1)}, {int64(1), int64(2)}}
		for _, prepared := range []bool{false, true} {
			run(referenceExec("ALTER SEQUENCE s RESTART WITH 1", 0))
			s := referenceSelect("SELECT k,id FROM collision ORDER BY id", []string{"k", "id"}, []string{"INT8", "INT4"}, original)
			s.prepared = prepared
			run(s)
			run(referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
		}
		for i, flag := range []string{"false", "true", "20"} {
			run(referenceExec("ALTER SEQUENCE s RESTART WITH 1", 0))
			d := deparseRelationsRead(t, p, b, c, "SELECT pg_get_viewdef('collision'::regclass,"+flag+") AS definition")
			name := fmt.Sprintf("collision_copy_%d", i)
			run(referenceExec("CREATE VIEW "+name+" AS "+d, 0))
			// Deparsing and compiling copies must not evaluate the volatile target.
			run(referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
			for _, prepared := range []bool{false, true} {
				run(referenceExec("ALTER SEQUENCE s RESTART WITH 1", 0))
				want := original
				if b.pg {
					// Independently recorded PG18 own-export fidelity bug: bare ORDER k
					// retargets the hidden merged key to the overridden volatile output.
					want = [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}
				}
				s := referenceSelect("SELECT k,id FROM "+name+" ORDER BY id", []string{"k", "id"}, []string{"INT8", "INT4"}, want)
				s.prepared = prepared
				run(s)
				run(referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
			}
			d = deparseRelationsRead(t, p, b, c, "SELECT pg_get_viewdef('merged_usage'::regclass,"+flag+") AS definition")
			name = fmt.Sprintf("merged_copy_%d", i)
			run(referenceExec("CREATE VIEW "+name+" AS "+d, 0))
			for _, prepared := range []bool{false, true} {
				s := referenceSelect("SELECT k,id FROM "+name+" ORDER BY id", []string{"k", "id"}, []string{"VARCHAR", "INT4"}, [][]any{{"b", int64(1)}, {"a", int64(2)}})
				s.prepared = prepared
				s.checkMetadata = func(t *testing.T, r *sql.Rows) {
					types, err := r.ColumnTypes()
					if err != nil || len(types) != 2 {
						t.Fatal(types, err)
					}
					want := int64(math.MaxInt64)
					if b.pg {
						want = -5
					}
					if length, known := types[0].Length(); !known || length != want {
						t.Fatal("merged output usage", length, known, "want", want)
					}
				}
				run(s)
			}
		}
	})
}
