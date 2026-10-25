package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInheritanceQueryConsumers(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		inheritanceQueryMapped(s)
		for _, query := range []string{
			"SELECT a FROM (SELECT a FROM p) sub ORDER BY a",
			"WITH src AS (SELECT a FROM p) SELECT a FROM src ORDER BY a",
			"SELECT p.a FROM p JOIN (VALUES(1),(2)) AS v(a) ON p.a=v.a ORDER BY p.a",
		} {
			s.values(query, []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
		}
		s.values("WITH unused AS (SELECT a FROM p) SELECT 99 AS marker", []string{"marker"}, []string{"INT4"}, [][]any{{int64(99)}})
		// Pinned PG18.0 treats all three forms as this CTE, not catalog lookup.
		for _, from := range []string{"ONLY src", "ONLY(src)", "src*"} {
			s.values("WITH src AS (SELECT 1 AS a) SELECT * FROM "+from, []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}})
		}
		s.values("SELECT count(*) AS n,sum(a) AS total FROM p", []string{"n", "total"}, []string{"INT8", "INT8"}, [][]any{{int64(2), int64(3)}})
		s.values("SELECT (SELECT count(*) FROM p) AS n,EXISTS(SELECT a FROM p WHERE a=2) AS present", []string{"n", "present"}, []string{"INT8", "BOOL"}, [][]any{{int64(2), true}})
		s.values("SELECT a FROM p WHERE a=2", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}})
		s.values("SELECT a FROM p ORDER BY a LIMIT 1", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}})
		s.exec("CREATE VIEW inherited_view AS SELECT a FROM p", 0)
		s.exec("CREATE TABLE copied AS SELECT a FROM p", 2)
		s.exec("CREATE MATERIALIZED VIEW materialized AS SELECT a FROM p", 2)
		for _, name := range []string{"inherited_view", "copied", "materialized"} {
			s.values("SELECT a FROM "+name+" ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
		}
		s.exec("CREATE TABLE sink(a INT)", 0)
		s.exec("INSERT INTO sink SELECT a FROM p", 2)
		s.values("SELECT * FROM sink ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
		s.exec("UPDATE sink SET a=p.a+10 FROM p WHERE sink.a=p.a", 2)
		s.values("SELECT * FROM sink ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(11)}, {int64(12)}})
		s.exec("DELETE FROM sink USING p WHERE sink.a=p.a+10", 2)
		s.values("SELECT * FROM sink", []string{"a"}, []string{"INT4"}, nil)
		merge := fixture.Exec("MERGE INTO sink d USING p s ON d.a=s.a WHEN NOT MATCHED THEN INSERT(a) VALUES(s.a)", 2)
		if s.b.PG {
			merge.Affected = 0 // Existing lib/pq MERGE export; rows below prove two inserts.
		}
		s.run(merge)
		s.values("SELECT * FROM sink ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
	})
}
