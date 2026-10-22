package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePartitionInheritedAtomicity(t *testing.T) {
	for _, tc := range []struct {
		name, action string
		null         bool
	}{
		{"add_check", "ADD CONSTRAINT pi_ck CHECK(b=1)", false},
		{"add_null", "ADD CONSTRAINT pi_nn NOT NULL a", true},
		{"set_null", "ALTER COLUMN a SET NOT NULL", true},
	} {
		for _, mixed := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "", true: "_late_action"}[mixed], func(t *testing.T) {
				partitionInheritedScenario(t, func(s *partitionInheritedCase) {
					s.tree(",CONSTRAINT pi_guard CHECK(k>=0)", "")
					s.exec("INSERT INTO pi_p VALUES(1,7,1),(11,NULL,2)", 2)
					before := make(map[string]string)
					for _, table := range []string{"pi_p", "pi_mid", "pi_lo", "pi_hi"} {
						before[table] = s.oid(table, "pi_guard")
					}
					q := "ALTER TABLE pi_p "
					if mixed {
						q += "DROP CONSTRAINT pi_guard,"
					}
					q += tc.action
					d := fixture.Diagnostic{Code: "23514", Message: `check constraint "pi_ck" of relation "pi_hi" is violated by some row`, Schema: "public", Table: "pi_hi", Constraint: "pi_ck"}
					if tc.null {
						d = fixture.Diagnostic{Code: "23502", Message: `column "a" of relation "pi_hi" contains null values`, Schema: "public", Table: "pi_hi", Column: "a"}
					}
					s.fail(q, d)
					for i, table := range []string{"pi_p", "pi_mid", "pi_lo", "pi_hi"} {
						count := int64(1)
						if i == 0 {
							count = 0
						}
						s.facts(table, [][]any{partitionInheritedFact("pi_guard", "c", i == 0, count, "{1}")})
						s.sameOID(table, "pi_guard", before[table])
						s.nullability(table, false)
					}
					s.values("SELECT k,a,b FROM pi_p ORDER BY k", [][]any{{int64(1), int64(7), int64(1)}, {int64(11), nil, int64(2)}}, []bool{true, true, true})
					// The earlier DROP must not become visible; fresh valid NULL/bad-b
					// rows also prove failed validation added no hidden enforcement.
					s.exec("INSERT INTO pi_p VALUES(2,NULL,2)", 1)
					s.fail("INSERT INTO pi_hi VALUES(-1,7,1)", partitionInheritedCheckFailure("pi_hi", "pi_guard", "-1, 7, 1"))
				})
			})
		}
	}
}

func TestReferencePartitionInheritedConflictAtomicity(t *testing.T) {
	for _, tc := range []struct{ name, declaration, state, message string }{
		{"mismatch", "CHECK(b=2)", "42710", `constraint "pi_ck" for relation "pi_mid" already exists`},
		{"noinherit", "CHECK(b=1) NO INHERIT", "42P17", `constraint "pi_ck" conflicts with non-inherited constraint on relation "pi_mid"`},
		{"notvalid", "CHECK(b=1) NOT VALID", "42P17", `constraint "pi_ck" conflicts with NOT VALID constraint on relation "pi_mid"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			partitionInheritedScenario(t, func(s *partitionInheritedCase) {
				// NOT VALID and NO INHERIT cannot be declared on a partitioned
				// intermediate in all forms. Put the conflicting fact on a leaf.
				s.exec("CREATE TABLE pi_p(k INT,a INT,b INT,CONSTRAINT pi_guard CHECK(k>=0)) PARTITION BY RANGE(k)", 0)
				s.exec("CREATE TABLE pi_lo PARTITION OF pi_p FOR VALUES FROM(0) TO(10)", 0)
				s.exec("CREATE TABLE pi_mid PARTITION OF pi_p FOR VALUES FROM(10) TO(20)", 0)
				s.exec("ALTER TABLE pi_mid ADD CONSTRAINT pi_ck "+tc.declaration, 0)
				old := s.oid("pi_mid", "pi_ck")
				guard := s.oid("pi_p", "pi_guard")
				s.fail("ALTER TABLE pi_p DROP CONSTRAINT pi_guard,ADD CONSTRAINT pi_ck CHECK(b=1)", fixture.Diagnostic{Code: tc.state, Message: tc.message})
				s.sameOID("pi_mid", "pi_ck", old)
				s.sameOID("pi_p", "pi_guard", guard)
				s.facts("pi_p", [][]any{partitionInheritedFact("pi_guard", "c", true, 0, "{1}")})
				s.facts("pi_lo", [][]any{partitionInheritedFact("pi_guard", "c", false, 1, "{1}")})
				local := partitionInheritedFact("pi_ck", "c", true, 0, "{3}")
				if tc.name == "noinherit" {
					local[4] = true
				}
				if tc.name == "notvalid" {
					local[5] = false
				}
				s.facts("pi_mid", [][]any{local, partitionInheritedFact("pi_guard", "c", false, 1, "{1}")})
			})
		})
	}
}

func TestReferencePartitionInheritedDiagnostics(t *testing.T) {
	partitionInheritedScenario(t, func(s *partitionInheritedCase) {
		s.tree("", "")
		for _, tc := range []struct{ action, code, message string }{
			{"ADD CONSTRAINT pi_bad CHECK(a)", "42804", "argument of CHECK must be type boolean, not type integer"},
			{"ADD CONSTRAINT pi_bad CHECK(missing>0)", "42703", `column "missing" does not exist`},
		} {
			q := "ALTER TABLE pi_p " + tc.action
			s.fail(q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
			s.facts("pi_p", nil)
			s.facts("pi_lo", nil)
		}
		q := "ALTER TABLE pi_p ADD CONSTRAINT pi_bad CHECK(b=1),"
		step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: "syntax error at end of input", Position: fixture.Position(len(q) + 1)})
		step.ErrorStage = "execute"
		if s.prepared {
			step.ErrorStage = "prepare"
		}
		s.run(step)
		s.facts("pi_p", nil)
		s.facts("pi_hi", nil)
	})
}
