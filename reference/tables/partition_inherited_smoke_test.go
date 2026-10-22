package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePartitionInheritedFeatureSmoke(t *testing.T) {
	partitionInheritedScenario(t, func(s *partitionInheritedCase) {
		t := s.t
		s.exec("CREATE TABLE pic_p(k INT,a INT CONSTRAINT pic_nn NOT NULL,b INT,CONSTRAINT pic_ck CHECK(b=1)) PARTITION BY RANGE(k)", 0)
		s.exec("CREATE TABLE pic_lo PARTITION OF pic_p FOR VALUES FROM(0) TO(10)", 0)
		s.exec("CREATE TABLE pic_hi PARTITION OF pic_p(a CONSTRAINT pic_nn NOT NULL) FOR VALUES FROM(10) TO(20)", 0)
		checks := make(map[string]string)
		nulls := make(map[string]string)
		seen := make(map[string]bool)
		for i, table := range []string{"pic_p", "pic_lo", "pic_hi"} {
			s.facts(table, [][]any{partitionInheritedFact("pic_ck", "c", i == 0, int64(min(i, 1)), "{3}"), partitionInheritedFact("pic_nn", "n", i != 1, int64(min(i, 1)), "{2}")})
			checks[table], nulls[table] = s.oid(table, "pic_ck"), s.oid(table, "pic_nn")
			for _, oid := range []string{checks[table], nulls[table]} {
				if seen[oid] {
					t.Fatal("physical constraints reused OID", oid)
				}
				seen[oid] = true
			}
		}
		// Each scenario exercises both actual public routes, not SQL PREPARE.
		fixture.RunStep(t, s.p.Ctx, s.conn, s.recorder, fixture.Exec("INSERT INTO pic_p VALUES($1,$2,$3)", 1, 1, 7, nil))
		step := fixture.Exec("INSERT INTO pic_p VALUES($1,$2,$3)", 1, 11, 8, 1)
		step.Prepared = true
		fixture.RunStep(t, s.p.Ctx, s.conn, s.recorder, step)
		s.values("SELECT k,a,b FROM pic_p ORDER BY k", [][]any{{int64(1), int64(7), nil}, {int64(11), int64(8), int64(1)}}, []bool{true, false, true})
		s.fail("INSERT INTO pic_p VALUES(12,7,2)", partitionInheritedCheckFailure("pic_hi", "pic_ck", "12, 7, 2"))
		s.fail("INSERT INTO pic_lo VALUES(2,NULL,1)", partitionInheritedNullFailure("pic_lo", "2, null, 1"))
		s.exec("ALTER TABLE pic_p ADD CONSTRAINT pic_extra CHECK(a>0)", 0)
		for i, table := range []string{"pic_p", "pic_lo", "pic_hi"} {
			s.facts(table, [][]any{partitionInheritedFact("pic_ck", "c", i == 0, int64(min(i, 1)), "{3}"), partitionInheritedFact("pic_extra", "c", i == 0, int64(min(i, 1)), "{2}"), partitionInheritedFact("pic_nn", "n", i != 1, int64(min(i, 1)), "{2}")})
		}
		s.fail("INSERT INTO pic_p VALUES(2,0,1)", partitionInheritedCheckFailure("pic_lo", "pic_extra", "2, 0, 1"))
		s.exec("ALTER TABLE pic_p DROP CONSTRAINT pic_extra", 0)
		for i, table := range []string{"pic_p", "pic_lo", "pic_hi"} {
			s.facts(table, [][]any{partitionInheritedFact("pic_ck", "c", i == 0, int64(min(i, 1)), "{3}"), partitionInheritedFact("pic_nn", "n", i != 1, int64(min(i, 1)), "{2}")})
			s.sameOID(table, "pic_ck", checks[table])
			s.sameOID(table, "pic_nn", nulls[table])
		}
		s.exec("ALTER TABLE pic_p DROP CONSTRAINT pic_nn", 0)
		check := func() {
			for i, table := range []string{"pic_p", "pic_lo", "pic_hi"} {
				facts := [][]any{partitionInheritedFact("pic_ck", "c", i == 0, int64(min(i, 1)), "{3}")}
				if i == 2 {
					facts = append(facts, partitionInheritedFact("pic_nn", "n", true, 0, "{2}"))
				}
				s.facts(table, facts)
				s.nullability(table, i == 2)
			}
			s.values("SELECT k,a,b FROM pic_p ORDER BY k", [][]any{{int64(1), int64(7), nil}, {int64(2), nil, nil}, {int64(11), int64(8), int64(1)}}, []bool{true, true, true})
			s.values("SELECT k,a,b FROM pic_hi WHERE false", nil, []bool{true, false, true})
			s.fail("INSERT INTO pic_p VALUES(12,7,2)", partitionInheritedCheckFailure("pic_hi", "pic_ck", "12, 7, 2"))
			s.fail("INSERT INTO pic_hi VALUES(12,NULL,1)", partitionInheritedNullFailure("pic_hi", "12, null, 1"))
		}
		for _, table := range []string{"pic_p", "pic_lo", "pic_hi"} {
			s.sameOID(table, "pic_ck", checks[table])
		}
		s.sameOID("pic_hi", "pic_nn", nulls["pic_hi"])
		s.exec("INSERT INTO pic_p VALUES(2,NULL,NULL)", 1)
		check()
		if s.b.PG {
			return
		} // Public native controls have no PG equivalent.
		var dump bytes.Buffer
		if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check()
		s.exec("INSERT INTO pic_lo VALUES(3,NULL,NULL)", 1)
		s.values("SELECT k,a,b FROM pic_lo ORDER BY k", [][]any{{int64(1), int64(7), nil}, {int64(2), nil, nil}, {int64(3), nil, nil}}, []bool{true, true, true})
	})
}
