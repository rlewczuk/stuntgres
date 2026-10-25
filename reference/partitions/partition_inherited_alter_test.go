package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePartitionInheritedAlter(t *testing.T) {
	for _, kind := range []string{"check", "named_null", "set_null"} {
		t.Run(kind, func(t *testing.T) {
			partitionInheritedScenario(t, func(s *partitionInheritedCase) {
				mid := "(a CONSTRAINT pi_child_nn NOT NULL)"
				add, drop := "ADD CONSTRAINT pi_nn NOT NULL a", "DROP CONSTRAINT pi_nn"
				name, typ, key := "pi_child_nn", "n", "{2}"
				if kind == "set_null" {
					add, drop = "ALTER COLUMN a SET NOT NULL", "ALTER COLUMN a DROP NOT NULL"
				}
				if kind == "check" {
					mid = "(CONSTRAINT pi_ck CHECK(b=1))"
					add, drop = "ADD CONSTRAINT pi_ck CHECK(b=1)", "DROP CONSTRAINT pi_ck"
					name, typ, key = "pi_ck", "c", "{3}"
				}
				s.tree("", mid)
				s.exec("INSERT INTO pi_p VALUES(1,7,1),(11,8,NULL)", 2)
				midOID, leafOID := s.oid("pi_mid", name), s.oid("pi_lo", name)
				s.notices = nil
				// A merged intermediate stops only its action; the independent CHECK
				// must still reach both leaves in this same statement.
				s.exec("ALTER TABLE pi_p "+add+",ADD CONSTRAINT pi_extra CHECK(k>=0)", 0)
				if kind == "check" {
					s.expectNotices(partitionInheritedMergeNotice("pi_ck"))
				} else {
					s.expectNotices()
				}
				rootName := "pi_nn"
				if kind == "set_null" {
					rootName = "pi_p_a_not_null"
				}
				if kind == "check" {
					rootName = "pi_ck"
				}
				s.facts("pi_p", [][]any{partitionInheritedFact(rootName, typ, true, 0, key), partitionInheritedFact("pi_extra", "c", true, 0, "{1}")})
				// pi_extra sorts before pi_nn, but after pi_ck.
				for _, table := range []string{"pi_mid", "pi_lo", "pi_hi"} {
					local := table == "pi_mid" && kind != "check"
					s.facts(table, [][]any{partitionInheritedFact(name, typ, local, 1, key), partitionInheritedFact("pi_extra", "c", false, 1, "{1}")})
				}
				s.sameOID("pi_mid", name, midOID)
				s.sameOID("pi_lo", name, leafOID)
				s.exec("ALTER TABLE pi_p "+drop+",DROP CONSTRAINT pi_extra", 0)
				s.facts("pi_p", nil)
				if kind == "check" {
					for _, table := range []string{"pi_mid", "pi_lo", "pi_hi"} {
						s.facts(table, nil)
					}
					s.exec("INSERT INTO pi_p VALUES(2,NULL,2)", 1)
				} else {
					s.facts("pi_mid", [][]any{partitionInheritedFact(name, "n", true, 0, "{2}")})
					s.facts("pi_lo", [][]any{partitionInheritedFact(name, "n", false, 1, "{2}")})
					s.sameOID("pi_mid", name, midOID)
					s.sameOID("pi_lo", name, leafOID)
					s.nullability("pi_p", false)
					s.nullability("pi_lo", true)
					s.fail("INSERT INTO pi_p VALUES(2,NULL,1)", partitionInheritedNullFailure("pi_lo", "2, null, 1"))
				}
			})
		})
	}
}

func TestReferencePartitionInheritedDrop(t *testing.T) {
	for _, action := range []string{"DROP CONSTRAINT pi_nn", "ALTER COLUMN a DROP NOT NULL"} {
		t.Run(action, func(t *testing.T) {
			partitionInheritedScenario(t, func(s *partitionInheritedCase) {
				s.exec("CREATE TABLE pi_p(k INT,a INT CONSTRAINT pi_nn NOT NULL,b INT,CONSTRAINT pi_ck CHECK(b=1)) PARTITION BY RANGE(k)", 0)
				s.exec("CREATE TABLE pi_mid PARTITION OF pi_p DEFAULT PARTITION BY RANGE(k)", 0)
				s.exec("CREATE TABLE pi_lo PARTITION OF pi_mid DEFAULT", 0)
				s.fail("ALTER TABLE pi_mid DROP CONSTRAINT pi_ck", fixture.Diagnostic{Code: "42P16", Message: `cannot drop inherited constraint "pi_ck" of relation "pi_mid"`})
				s.fail("ALTER TABLE pi_mid DROP CONSTRAINT pi_nn", fixture.Diagnostic{Code: "42P16", Message: `cannot drop inherited constraint "pi_nn" of relation "pi_mid"`})
				s.fail("ALTER TABLE pi_mid ALTER COLUMN a DROP NOT NULL", fixture.Diagnostic{Code: "42P16", Message: `column "a" is marked NOT NULL in parent table`})
				s.exec("ALTER TABLE pi_p DROP CONSTRAINT pi_ck,"+action, 0)
				for _, table := range []string{"pi_p", "pi_mid", "pi_lo"} {
					s.facts(table, nil)
					s.nullability(table, false)
				}
				s.exec("INSERT INTO pi_p VALUES(1,NULL,2)", 1)
				s.values("SELECT k,a,b FROM pi_p", [][]any{{int64(1), nil, int64(2)}}, []bool{true, true, true})
			})
		})
	}
}

func TestReferencePartitionInheritedOnly(t *testing.T) {
	for _, tc := range []struct{ name, action, constraint, typ, key string }{
		{"check", "DROP CONSTRAINT pi_ck", "pi_ck", "c", "{3}"},
		{"named_null", "DROP CONSTRAINT pi_nn", "pi_nn", "n", "{2}"},
		{"column_null", "ALTER COLUMN a DROP NOT NULL", "pi_nn", "n", "{2}"},
	} {
		t.Run("drop_"+tc.name, func(t *testing.T) {
			partitionInheritedScenario(t, func(s *partitionInheritedCase) {
				definition := "k INT,a INT CONSTRAINT pi_nn NOT NULL,b INT"
				if tc.typ == "c" {
					definition = "k INT,a INT,b INT,CONSTRAINT pi_ck CHECK(b=1)"
				}
				s.exec("CREATE TABLE pi_p("+definition+") PARTITION BY RANGE(k)", 0)
				s.exec("CREATE TABLE pi_mid PARTITION OF pi_p DEFAULT PARTITION BY RANGE(k)", 0)
				s.exec("CREATE TABLE pi_lo PARTITION OF pi_mid DEFAULT", 0)
				midOID, leafOID := s.oid("pi_mid", tc.constraint), s.oid("pi_lo", tc.constraint)
				// Source prediction, not observed oracle data: ONLY removal localizes
				// the immediate child and leaves its own descendants inherited.
				s.exec("ALTER TABLE ONLY pi_p "+tc.action, 0)
				s.facts("pi_p", nil)
				s.facts("pi_mid", [][]any{partitionInheritedFact(tc.constraint, tc.typ, true, 0, tc.key)})
				s.facts("pi_lo", [][]any{partitionInheritedFact(tc.constraint, tc.typ, false, 1, tc.key)})
				s.sameOID("pi_mid", tc.constraint, midOID)
				s.sameOID("pi_lo", tc.constraint, leafOID)
				if tc.typ == "c" {
					s.fail("INSERT INTO pi_p VALUES(1,7,2)", partitionInheritedCheckFailure("pi_lo", "pi_ck", "1, 7, 2"))
				} else {
					s.nullability("pi_p", false)
					s.nullability("pi_mid", true)
					s.nullability("pi_lo", true)
					s.fail("INSERT INTO pi_p VALUES(1,NULL,1)", partitionInheritedNullFailure("pi_lo", "1, null, 1"))
				}
				s.exec("ALTER TABLE pi_mid "+tc.action, 0)
				s.facts("pi_mid", nil)
				s.facts("pi_lo", nil)
				s.exec("INSERT INTO pi_p VALUES(1,NULL,2)", 1)
			})
		})
	}
	for _, tc := range []struct {
		name, action string
		hint         bool
	}{
		{"check", "ADD CONSTRAINT pi_ck CHECK(b=1)", false},
		{"named_null", "ADD CONSTRAINT pi_nn NOT NULL a", false},
		{"set_null", "ALTER COLUMN a SET NOT NULL", true},
	} {
		for _, children := range []bool{false, true} {
			t.Run("fresh_"+tc.name+map[bool]string{false: "_childless", true: "_children"}[children], func(t *testing.T) {
				partitionInheritedScenario(t, func(s *partitionInheritedCase) {
					s.exec("CREATE TABLE pi_p(k INT,a INT,b INT) PARTITION BY RANGE(k)", 0)
					if children {
						s.exec("CREATE TABLE pi_lo PARTITION OF pi_p DEFAULT", 0)
					}
					q := "ALTER TABLE ONLY pi_p " + tc.action
					if children {
						d := fixture.Diagnostic{Code: "42P16", Message: "constraint must be added to child tables too"}
						if tc.hint {
							d.Hint = "Do not specify the ONLY keyword."
						}
						s.fail(q, d)
						s.facts("pi_p", nil)
						s.facts("pi_lo", nil)
						s.nullability("pi_lo", false)
					} else {
						s.exec(q, 0)
						// Childless ONLY is a genuine positive, not just syntax acceptance.
						name, typ, key := "pi_ck", "c", "{3}"
						if tc.name != "check" {
							name, typ, key = "pi_nn", "n", "{2}"
						}
						if tc.name == "set_null" {
							name = "pi_p_a_not_null"
						}
						s.facts("pi_p", [][]any{partitionInheritedFact(name, typ, true, 0, key)})
						s.exec("ALTER TABLE ONLY pi_p DROP CONSTRAINT "+name, 0)
						s.facts("pi_p", nil)
					}
				})
			})
		}
	}
}

func TestReferencePartitionInheritedExistingSet(t *testing.T) {
	for _, only := range []bool{false, true} {
		t.Run(map[bool]string{false: "recursive", true: "only"}[only], func(t *testing.T) {
			partitionInheritedScenario(t, func(s *partitionInheritedCase) {
				s.exec("CREATE TABLE pi_p(k INT,a INT CONSTRAINT pi_nn NOT NULL,b INT) PARTITION BY RANGE(k)", 0)
				s.exec("CREATE TABLE pi_mid PARTITION OF pi_p DEFAULT PARTITION BY RANGE(k)", 0)
				s.exec("CREATE TABLE pi_lo PARTITION OF pi_mid DEFAULT", 0)
				midOID, leafOID := s.oid("pi_mid", "pi_nn"), s.oid("pi_lo", "pi_nn")
				prefix := "ALTER TABLE "
				if only {
					prefix += "ONLY "
				}
				s.exec(prefix+"pi_p ALTER COLUMN a SET NOT NULL", 0)
				s.exec(prefix+"pi_mid ALTER COLUMN a SET NOT NULL", 0)
				s.exec(prefix+"pi_mid ALTER COLUMN a SET NOT NULL", 0)
				s.facts("pi_p", [][]any{partitionInheritedFact("pi_nn", "n", true, 0, "{2}")})
				s.facts("pi_mid", [][]any{partitionInheritedFact("pi_nn", "n", true, 1, "{2}")})
				s.facts("pi_lo", [][]any{partitionInheritedFact("pi_nn", "n", false, 1, "{2}")})
				s.sameOID("pi_mid", "pi_nn", midOID)
				s.sameOID("pi_lo", "pi_nn", leafOID)
				s.exec("ALTER TABLE pi_p DROP CONSTRAINT pi_nn", 0)
				s.facts("pi_p", nil)
				s.facts("pi_mid", [][]any{partitionInheritedFact("pi_nn", "n", true, 0, "{2}")})
				s.fail("INSERT INTO pi_p VALUES(1,NULL,1)", partitionInheritedNullFailure("pi_lo", "1, null, 1"))
			})
		})
	}
}
