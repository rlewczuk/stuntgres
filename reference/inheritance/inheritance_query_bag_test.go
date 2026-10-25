package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInheritanceQueryBagDiamond(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		fixture.InheritanceQueryDiamond(s.run)
		for _, from := range []string{"p", "p*"} {
			step := fixture.Select("SELECT * FROM "+from, []string{"a", "x"}, []string{"INT4", "TEXT"}, fixture.InheritanceQueryFive())
			step.Bag = true
			s.run(step)
		}
		s.values("SELECT DISTINCT a FROM p", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})
		for _, tc := range []struct {
			query string
			count int64
		}{
			{"SELECT count(*) AS n FROM p", 5},
			{"SELECT count(*) AS n FROM p a JOIN ONLY p b ON a.a=b.a", 10},
			{"SELECT count(*) AS n FROM p a JOIN p b ON a.a=b.a", 25},
		} {
			s.values(tc.query, []string{"n"}, []string{"INT8"}, [][]any{{tc.count}})
		}
		s.values("SELECT * FROM c ORDER BY z", []string{"y", "a", "x", "z"}, []string{"BOOL", "INT4", "TEXT", "INT8"}, [][]any{{true, int64(7), "same", int64(9)}, {true, int64(7), "same", int64(10)}})
		s.exec("CREATE TABLE shortcut() INHERITS(p,l)", 0)
		s.exec("INSERT INTO shortcut VALUES(8,'shortcut')", 1)
		s.values("SELECT a FROM p WHERE a=8", []string{"a"}, []string{"INT4"}, [][]any{{int64(8)}})
		s.exec("CREATE TABLE zero_parent()", 0)
		s.exec("CREATE TABLE zero_child() INHERITS(zero_parent)", 0)
		s.exec("INSERT INTO zero_parent DEFAULT VALUES", 1)
		s.exec("INSERT INTO zero_child DEFAULT VALUES", 1)
		s.exec("INSERT INTO zero_child DEFAULT VALUES", 1)
		s.values("SELECT count(*) AS n FROM zero_parent", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}})
		step := fixture.Select("SELECT FROM zero_parent", []string{}, []string{}, [][]any{{}, {}, {}})
		step.Bag = true
		s.run(step)
	})
}
