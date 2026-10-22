package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePartitionInheritedBoundary(t *testing.T) {
	partitionInheritedScenario(t, func(s *partitionInheritedCase) {
		s.tree(",CONSTRAINT pi_guard CHECK(k>=0)", "")
		s.exec("INSERT INTO pi_p VALUES(1,7,1),(11,8,NULL)", 2)
		before := make(map[string]string)
		for _, table := range []string{"pi_p", "pi_mid", "pi_lo", "pi_hi"} {
			before[table] = s.oid(table, "pi_guard")
		}
		q := "ALTER TABLE pi_p DROP CONSTRAINT pi_guard,ADD COLUMN extra INT"
		if s.b.PG {
			s.exec(q, 0)
		} else {
			// Column hierarchy ALTER is an explicit native capability boundary;
			// a supported constraint prefix must not publish before that error.
			s.fail(q, fixture.Diagnostic{Code: "0A000", Message: "altering partition hierarchies is unsupported"})
		}
		for i, table := range []string{"pi_p", "pi_mid", "pi_lo", "pi_hi"} {
			var facts, columns [][]any
			if s.b.PG {
				columns = [][]any{{"extra", false}}
			} else {
				facts = [][]any{partitionInheritedFact("pi_guard", "c", i == 0, int64(min(i, 1)), "{1}")}
				s.sameOID(table, "pi_guard", before[table])
			}
			s.facts(table, facts)
			s.run(fixture.Select("SELECT attname,attnotnull FROM pg_attribute WHERE attrelid='"+table+"'::regclass::oid AND attname='extra'", []string{"attname", "attnotnull"}, []string{"NAME", "BOOL"}, columns))
		}
		s.values("SELECT k,a,b FROM pi_p ORDER BY k", [][]any{{int64(1), int64(7), int64(1)}, {int64(11), int64(8), nil}}, []bool{true, true, true})
		if !s.b.PG {
			s.fail("INSERT INTO pi_lo VALUES(-1,7,1)", partitionInheritedCheckFailure("pi_lo", "pi_guard", "-1, 7, 1"))
		}
	})
}
