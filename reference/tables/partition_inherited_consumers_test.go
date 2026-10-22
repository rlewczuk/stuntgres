package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePartitionInheritedAddNames(t *testing.T) {
	for _, name := range []string{"pi_nn", "pi_different_nn"} {
		t.Run(name, func(t *testing.T) {
			partitionInheritedScenario(t, func(s *partitionInheritedCase) {
				s.exec("CREATE TABLE pi_p(k INT,a INT CONSTRAINT pi_nn NOT NULL,b INT) PARTITION BY RANGE(k)", 0)
				s.exec("CREATE TABLE pi_mid PARTITION OF pi_p DEFAULT PARTITION BY RANGE(k)", 0)
				s.exec("CREATE TABLE pi_lo PARTITION OF pi_mid DEFAULT", 0)
				old := s.oid("pi_mid", "pi_nn")
				leaf := s.oid("pi_lo", "pi_nn")
				root := s.oid("pi_p", "pi_nn")
				if name == "pi_different_nn" {
					s.fail("ALTER TABLE pi_mid ADD CONSTRAINT "+name+" NOT NULL a", fixture.Diagnostic{
						Code:    "55000",
						Message: `cannot create not-null constraint "pi_different_nn" on column "a" of table "pi_mid"`,
						Detail:  `A not-null constraint named "pi_nn" already exists for this column.`,
					})
					for i, table := range []string{"pi_p", "pi_mid", "pi_lo"} {
						s.facts(table, [][]any{partitionInheritedFact("pi_nn", "n", i == 0, int64(min(i, 1)), "{2}")})
						s.nullability(table, true)
					}
					s.sameOID("pi_p", "pi_nn", root)
					s.sameOID("pi_mid", "pi_nn", old)
					s.sameOID("pi_lo", "pi_nn", leaf)
					s.values("SELECT k,a,b FROM pi_p", nil, []bool{true, false, true})
					s.fail("INSERT INTO pi_p VALUES(1,NULL,1)", partitionInheritedNullFailure("pi_lo", "1, null, 1"))
					s.exec("ALTER TABLE pi_p DROP CONSTRAINT pi_nn", 0)
					for _, table := range []string{"pi_p", "pi_mid", "pi_lo"} {
						s.facts(table, nil)
						s.nullability(table, false)
					}
					s.exec("INSERT INTO pi_p VALUES(1,NULL,1)", 1)
					return
				}
				s.exec("ALTER TABLE pi_mid ADD CONSTRAINT "+name+" NOT NULL a", 0)
				s.facts("pi_mid", [][]any{partitionInheritedFact("pi_nn", "n", true, 1, "{2}")})
				s.facts("pi_lo", [][]any{partitionInheritedFact("pi_nn", "n", false, 1, "{2}")})
				s.sameOID("pi_mid", "pi_nn", old)
				s.sameOID("pi_lo", "pi_nn", leaf)
				s.exec("ALTER TABLE pi_p DROP CONSTRAINT pi_nn", 0)
				s.facts("pi_mid", [][]any{partitionInheritedFact("pi_nn", "n", true, 0, "{2}")})
				s.sameOID("pi_mid", "pi_nn", old)
				s.fail("INSERT INTO pi_p VALUES(1,NULL,1)", partitionInheritedNullFailure("pi_lo", "1, null, 1"))
			})
		})
	}
}

func TestReferencePartitionInheritedConsumers(t *testing.T) {
	partitionInheritedScenario(t, func(s *partitionInheritedCase) {
		s.tree("", "")
		insertSQL := "INSERT INTO pi_p VALUES($1,$2,$3)"
		selectSQL := "SELECT k,a,b FROM pi_p ORDER BY k"
		insert, err := s.conn.PrepareContext(s.p.Ctx, insertSQL)
		if err != nil {
			s.t.Fatal(err)
		}
		defer insert.Close()
		read, err := s.conn.PrepareContext(s.p.Ctx, selectSQL)
		if err != nil {
			s.t.Fatal(err)
		}
		defer read.Close()
		heldInsert := fixture.HeldStatement{Executor: s.conn, Statement: insert, SQL: insertSQL}
		heldRead := fixture.HeldStatement{Executor: s.conn, Statement: read, SQL: selectSQL}
		values := func(rows [][]any, notnull bool) {
			s.t.Helper()
			step := fixture.Select(selectSQL, []string{"k", "a", "b"}, []string{"INT4", "INT4", "INT4"}, rows)
			step.CheckMetadata = referenceRangeMetadata(s.b.PG, step.Columns, step.Types, []bool{true, !notnull, true})
			fixture.RunStep(s.t, s.p.Ctx, heldRead, s.recorder, step)
		}
		values(nil, false)
		s.exec("ALTER TABLE pi_p ALTER COLUMN a SET NOT NULL,ADD CONSTRAINT pi_ck CHECK(b=1)", 0)
		values(nil, true)
		fixture.RunStep(s.t, s.p.Ctx, heldInsert, s.recorder, fixture.Exec(insertSQL, 1, 1, 7, nil))
		bad := fixture.ImplicitError(insertSQL, partitionInheritedNullFailure("pi_hi", "11, null, 1"), 11, nil, 1)
		bad.ErrorStage = "execute"
		fixture.RunStep(s.t, s.p.Ctx, heldInsert, s.recorder, bad)
		bad = fixture.ImplicitError(insertSQL, partitionInheritedCheckFailure("pi_hi", "pi_ck", "11, 7, 2"), 11, 7, 2)
		bad.ErrorStage = "execute"
		fixture.RunStep(s.t, s.p.Ctx, heldInsert, s.recorder, bad)
		values([][]any{{int64(1), int64(7), nil}}, true)
		s.exec("ALTER TABLE pi_p DROP CONSTRAINT pi_ck,ALTER COLUMN a DROP NOT NULL", 0)
		fixture.RunStep(s.t, s.p.Ctx, heldInsert, s.recorder, fixture.Exec(insertSQL, 1, 11, nil, 2))
		values([][]any{{int64(1), int64(7), nil}, {int64(11), nil, int64(2)}}, false)
	})
}
