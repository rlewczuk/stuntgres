package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInheritanceQueryOptimization(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		inheritanceQueryMapped(s)
		s.exec("CREATE INDEX parent_hash ON p USING hash(a)", 0)
		s.exec("CREATE INDEX parent_partial ON p(a) WHERE a=7", 0)
		s.exec("INSERT INTO c VALUES(false,7,'only child',77)", 1)
		for _, query := range []string{"SELECT a FROM p WHERE a=7", "SELECT a FROM p WHERE a=$1"} {
			step := fixture.Select(query, []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})
			if query == "SELECT a FROM p WHERE a=$1" {
				step.Args = []any{7}
			}
			s.run(step)
		}
		s.values("SELECT a FROM ONLY p WHERE a=7", []string{"a"}, []string{"INT4"}, nil)
		s.exec("BEGIN", 0)
		s.exec("INSERT INTO c VALUES(false,8,'private child',88)", 1)
		s.values("SELECT a FROM p WHERE a=8", []string{"a"}, []string{"INT4"}, [][]any{{int64(8)}})
		s.exec("ROLLBACK", 0)
		s.values("SELECT a FROM p WHERE a=8", []string{"a"}, []string{"INT4"}, nil)
		s.exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
		s.values("SELECT a FROM p WHERE a=7", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})
		s.values("SELECT a FROM ONLY p WHERE a=7", []string{"a"}, []string{"INT4"}, nil)
		s.exec("ROLLBACK", 0)
	})
}
