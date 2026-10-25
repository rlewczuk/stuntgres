package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePartitionInheritedCreation(t *testing.T) {
	partitionInheritedScenario(t, func(s *partitionInheritedCase) {
		s.exec("CREATE TABLE pi_p(k INT,a INT CONSTRAINT pi_nn NOT NULL,b INT,CONSTRAINT pi_ck CHECK(b=1)) PARTITION BY RANGE(k)", 0)
		s.notices = nil
		s.exec("CREATE TABLE pi_mid PARTITION OF pi_p(a CONSTRAINT pi_nn NOT NULL,CONSTRAINT pi_ck CHECK(b=1)) DEFAULT PARTITION BY RANGE(k)", 0)
		s.expectNotices(partitionInheritedMergeNotice("pi_ck"))
		s.exec("CREATE TABLE pi_lo PARTITION OF pi_mid FOR VALUES FROM(0) TO(10)", 0)
		s.exec("CREATE TABLE pi_hi PARTITION OF pi_mid FOR VALUES FROM(10) TO(20)", 0)
		seen := make(map[string]bool)
		for _, tc := range []struct {
			table                 string
			checkLocal, nullLocal bool
			count                 int64
		}{
			{"pi_p", true, true, 0}, {"pi_mid", false, true, 1}, {"pi_lo", false, false, 1}, {"pi_hi", false, false, 1},
		} {
			s.facts(tc.table, [][]any{partitionInheritedFact("pi_ck", "c", tc.checkLocal, tc.count, "{3}"), partitionInheritedFact("pi_nn", "n", tc.nullLocal, tc.count, "{2}")})
			s.nullability(tc.table, true)
			for _, name := range []string{"pi_ck", "pi_nn"} {
				oid := s.oid(tc.table, name)
				if seen[oid] {
					s.t.Fatalf("clone reused constraint OID %s", oid)
				}
				seen[oid] = true
			}
			s.run(fixture.Select("SELECT relchecks FROM pg_class WHERE oid='"+tc.table+"'::regclass::oid", []string{"relchecks"}, []string{"INT2"}, [][]any{{int64(1)}}))
		}
		s.values("SELECT k,a,b FROM pi_p WHERE false", nil, []bool{true, false, true})
		for _, target := range []string{"pi_p", "pi_mid", "pi_lo"} {
			s.exec("INSERT INTO "+target+" VALUES($1,$2,$3)", 1, 1, 7, nil)
			s.fail("INSERT INTO "+target+" VALUES($1,$2,$3)", partitionInheritedNullFailure("pi_lo", "2, null, 1"), 2, nil, 1)
		}
	})
}

func TestReferencePartitionInheritedEnforcement(t *testing.T) {
	partitionInheritedScenario(t, func(s *partitionInheritedCase) {
		s.exec("CREATE TABLE pi_p(k INT,a INT CONSTRAINT pi_nn NOT NULL,b INT,CONSTRAINT pi_ck CHECK(b=1)) PARTITION BY RANGE(k)", 0)
		s.exec("CREATE TABLE pi_mid PARTITION OF pi_p DEFAULT PARTITION BY RANGE(k)", 0)
		s.exec("CREATE TABLE pi_lo PARTITION OF pi_mid FOR VALUES FROM(0) TO(10)", 0)
		s.exec("CREATE TABLE pi_hi PARTITION OF pi_mid FOR VALUES FROM(10) TO(20)", 0)
		s.exec("INSERT INTO pi_p VALUES($1,$2,$3)", 1, 1, 7, nil)
		s.exec("INSERT INTO pi_hi VALUES($1,$2,$3)", 1, 11, 8, 1)
		for _, tc := range []struct {
			target, leaf string
			k            int
		}{{"pi_p", "pi_hi", 12}, {"pi_mid", "pi_lo", 2}, {"pi_lo", "pi_lo", 2}} {
			s.fail("INSERT INTO "+tc.target+" VALUES("+map[int]string{12: "12", 2: "2"}[tc.k]+",7,2)", partitionInheritedCheckFailure(tc.leaf, "pi_ck", map[int]string{12: "12", 2: "2"}[tc.k]+", 7, 2"))
		}
		// A valid prefix reaches another physical leaf before the bad suffix.
		s.fail("INSERT INTO pi_p VALUES(2,9,1),(12,9,2)", partitionInheritedCheckFailure("pi_hi", "pi_ck", "12, 9, 2"))
		s.fail("INSERT INTO pi_mid VALUES(3,9,1),(13,NULL,1)", partitionInheritedNullFailure("pi_hi", "13, null, 1"))
		s.values("SELECT k,a,b FROM pi_p ORDER BY k", [][]any{{int64(1), int64(7), nil}, {int64(11), int64(8), int64(1)}}, []bool{true, false, true})
	})
}

// Existing RangePartitionCheckMerge owns same-name structural mismatch,
// NO INHERIT precedence and exact merging notices; do not duplicate its matrix.
func TestReferencePartitionInheritedNames(t *testing.T) {
	for _, declaration := range []struct {
		name, sql, surviving string
		local                bool
	}{
		{"omitted", "", "pi_nn", false},
		{"unnamed", "a NOT NULL", "pi_child_a_not_null", true},
		{"same", "a CONSTRAINT pi_nn NOT NULL", "pi_nn", true},
		{"different", "a CONSTRAINT pi_child_nn NOT NULL", "pi_child_nn", true},
	} {
		t.Run(declaration.name, func(t *testing.T) {
			partitionInheritedScenario(t, func(s *partitionInheritedCase) {
				s.exec("CREATE TABLE pi_p(k INT,a INT CONSTRAINT pi_nn NOT NULL,b INT,CONSTRAINT pi_ck CHECK(b=1)) PARTITION BY RANGE(k)", 0)
				definition := "CONSTRAINT pi_extra CHECK(b=1),CHECK(b=1)"
				if declaration.sql != "" {
					definition = declaration.sql + "," + definition
				}
				s.exec("CREATE TABLE pi_child PARTITION OF pi_p("+definition+") DEFAULT", 0)
				// Naming/locality predictions are deliberately independent, pending PG18.6.
				rows := [][]any{
					partitionInheritedFact("pi_child_b_check", "c", true, 0, "{3}"),
					partitionInheritedFact("pi_ck", "c", false, 1, "{3}"),
					partitionInheritedFact("pi_extra", "c", true, 0, "{3}"),
				}
				rows = append(rows, partitionInheritedFact(declaration.surviving, "n", declaration.local, 1, "{2}"))
				s.facts("pi_child", rows)
				s.exec("ALTER TABLE pi_p DROP CONSTRAINT pi_nn", 0)
				s.nullability("pi_p", false)
				s.nullability("pi_child", declaration.local)
				if declaration.local {
					s.fail("INSERT INTO pi_child VALUES(1,NULL,1)", partitionInheritedNullFailure("pi_child", "1, null, 1"))
				} else {
					s.exec("INSERT INTO pi_child VALUES(1,NULL,NULL)", 1)
				}
			})
		})
	}
}

func TestReferencePartitionInheritedMethods(t *testing.T) {
	for _, method := range []struct{ name, key, bound string }{{"list", "LIST(k)", "FOR VALUES IN(1)"}, {"hash", "HASH(k)", "FOR VALUES WITH(MODULUS 1,REMAINDER 0)"}} {
		t.Run(method.name, func(t *testing.T) {
			partitionInheritedScenario(t, func(s *partitionInheritedCase) {
				s.exec("CREATE TABLE pi_p(k INT,a INT,b INT) PARTITION BY "+method.key, 0)
				s.exec("CREATE TABLE pi_child PARTITION OF pi_p "+method.bound, 0)
				s.exec("ALTER TABLE pi_p ADD CONSTRAINT pi_ck CHECK(b=1),ALTER COLUMN a SET NOT NULL", 0)
				s.fail("INSERT INTO pi_p VALUES(1,7,2)", partitionInheritedCheckFailure("pi_child", "pi_ck", "1, 7, 2"))
				s.fail("INSERT INTO pi_child VALUES(1,NULL,1)", partitionInheritedNullFailure("pi_child", "1, null, 1"))
				s.exec("ALTER TABLE pi_p DROP CONSTRAINT pi_ck,ALTER COLUMN a DROP NOT NULL", 0)
				s.facts("pi_child", nil)
				s.exec("INSERT INTO pi_p VALUES(1,NULL,2)", 1)
			})
		})
	}
}
