package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTableLikeTemporaryRetainedLiterals(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		run := func(step fixture.Step) {
			step.Prepared = s.Prepared
			fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, step)
		}
		s.Utility("CREATE TEMP TABLE literal_anchor(v INT)", nil)
		s.Utility("CREATE TEMP TABLE literal_source(a INT,r BOOL GENERATED ALWAYS AS(a>0 AND ('pg_temp.literal_anchor'::regclass IS NOT NULL)) STORED,active BOOL,CONSTRAINT positive CHECK(a>0 AND ('pg_temp.literal_anchor'::regclass IS NOT NULL)))", nil)
		s.Utility("CREATE INDEX literal_predicate ON literal_source(a) WHERE active AND ('pg_temp.literal_anchor'::regclass IS NOT NULL)", nil)
		run(fixture.Exec("INSERT INTO literal_source(a,active) VALUES(3,true)", 1))
		if !s.Postgres {
			var dump bytes.Buffer
			if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &dump); err != nil {
				s.T.Fatal(err)
			}
			if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil {
				s.T.Fatal(err)
			}
			if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(dump.Bytes())); err != nil {
				s.T.Fatal(err)
			}
		}
		s.Utility("CREATE TEMP TABLE literal_copy(lead TEXT,LIKE pg_temp.literal_source INCLUDING ALL)", nil)
		s.Utility("ALTER TABLE literal_anchor RENAME TO renamed_anchor", nil)
		run(fixture.Exec("INSERT INTO literal_copy(lead,a,active) VALUES('x',7,true)", 1))
		run(fixture.Exec("UPDATE literal_copy SET a=8", 1))
		run(fixture.Select("SELECT * FROM literal_copy", []string{"lead", "a", "r", "active"}, []string{"TEXT", "INT4", "BOOL", "BOOL"}, [][]any{{"x", int64(8), true, true}}))
		run(fixture.Select("SELECT * FROM literal_source", []string{"a", "r", "active"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(3), true, true}}))
		run(fixture.TableLikeError("DROP TABLE renamed_anchor", fixture.Diagnostic{
			Code: "2BP01", Message: "cannot drop table renamed_anchor because other objects depend on it",
			Detail: "column r of table literal_source depends on table renamed_anchor\n" +
				"constraint positive on table literal_source depends on table renamed_anchor\n" +
				"index literal_predicate depends on table renamed_anchor\n" +
				"column r of table literal_copy depends on table renamed_anchor\n" +
				"constraint positive on table literal_copy depends on table renamed_anchor\n" +
				"index literal_copy_a_idx depends on table renamed_anchor",
			Hint: "Use DROP ... CASCADE to drop the dependent objects too.",
		}))
		s.Utility("DROP TABLE literal_copy", nil)
		s.Utility("DROP TABLE literal_source", nil)
		s.Utility("DROP TABLE renamed_anchor", nil)
	})
}
