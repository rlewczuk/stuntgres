package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRestrictRelationKindRuleCTE(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		s.run(fixture.Exec("CREATE TABLE rk_rule_t(id integer)", 0))
		s.run(fixture.Exec("INSERT INTO rk_rule_t VALUES(1),(2)", 2))
		for _, name := range []string{"rk_rule_v", "rk_rule_read", "rk_rule_read_target"} {
			s.run(fixture.Exec("CREATE VIEW "+name+" AS SELECT id FROM rk_rule_t", 0))
		}
		s.run(fixture.Exec("CREATE RULE rk_insert AS ON INSERT TO rk_rule_v DO INSTEAD INSERT INTO rk_rule_t VALUES(NEW.id) RETURNING id", 0))
		s.run(fixture.Exec("CREATE RULE rk_insert_read AS ON INSERT TO rk_rule_read_target DO INSTEAD INSERT INTO rk_rule_t SELECT id FROM rk_rule_read RETURNING id", 0))
		rkSet(s, "view")
		// The unconditional rule substitutes NEW and writes an ordinary table;
		// its eliminated view target is not an automatic view expansion.
		direct := "WITH inserted AS (INSERT INTO rk_rule_v VALUES(3) RETURNING id) SELECT id FROM inserted"
		s.run(fixture.Select(direct, []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}}))
		s.run(rkRows("rk_rule_t", 1, 2, 3))
		prepared := "WITH inserted AS (INSERT INTO rk_rule_v VALUES(4) RETURNING id) SELECT id FROM inserted"
		s.run(fixture.Prepared(fixture.Select(prepared, []string{"id"}, []string{"INT4"}, [][]any{{int64(4)}})))
		s.run(rkRows("rk_rule_t", 1, 2, 3, 4))
		// A rule does not exempt an actual view read in its surviving action.
		denied := "WITH inserted AS (INSERT INTO rk_rule_read_target VALUES(9) RETURNING id) SELECT id FROM inserted"
		for _, prepare := range []bool{false, true} {
			s.run(rkDenied(denied, "rk_rule_read", prepare, true))
			s.run(rkRows("rk_rule_t", 1, 2, 3, 4))
		}
		for _, prepare := range []bool{false, true} {
			s.run(rkDenied("SELECT id FROM rk_rule_v ORDER BY id", "rk_rule_v", prepare, true))
		}
		s.run(fixture.Exec("RESET "+rkSetting, 0))
		s.run(rkRows("rk_rule_v", 1, 2, 3, 4))
	})
}
