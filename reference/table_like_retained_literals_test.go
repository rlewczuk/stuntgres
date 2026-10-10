package reference_test

import (
	"bytes"
	"stuntgres"
	"testing"
)

func TestReferenceTableLikeTemporaryRetainedLiterals(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		run := func(step referenceStep) {
			step.prepared = s.prepared
			referenceRunStep(s.t, s.p.ctx, s.c, s.record, step)
		}
		s.utility("CREATE TEMP TABLE literal_anchor(v INT)", nil)
		s.utility("CREATE TEMP TABLE literal_source(a INT,r BOOL GENERATED ALWAYS AS(a>0 AND ('pg_temp.literal_anchor'::regclass IS NOT NULL)) STORED,active BOOL,CONSTRAINT positive CHECK(a>0 AND ('pg_temp.literal_anchor'::regclass IS NOT NULL)))", nil)
		s.utility("CREATE INDEX literal_predicate ON literal_source(a) WHERE active AND ('pg_temp.literal_anchor'::regclass IS NOT NULL)", nil)
		run(referenceExec("INSERT INTO literal_source(a,active) VALUES(3,true)", 1))
		if !s.postgres {
			var dump bytes.Buffer
			if err := stuntgres.Dump(s.p.ctx, s.p.native, &dump); err != nil {
				s.t.Fatal(err)
			}
			if err := stuntgres.Reset(s.p.ctx, s.p.native); err != nil {
				s.t.Fatal(err)
			}
			if err := stuntgres.Restore(s.p.ctx, s.p.native, bytes.NewReader(dump.Bytes())); err != nil {
				s.t.Fatal(err)
			}
		}
		s.utility("CREATE TEMP TABLE literal_copy(lead TEXT,LIKE pg_temp.literal_source INCLUDING ALL)", nil)
		s.utility("ALTER TABLE literal_anchor RENAME TO renamed_anchor", nil)
		run(referenceExec("INSERT INTO literal_copy(lead,a,active) VALUES('x',7,true)", 1))
		run(referenceExec("UPDATE literal_copy SET a=8", 1))
		run(referenceSelect("SELECT * FROM literal_copy", []string{"lead", "a", "r", "active"}, []string{"TEXT", "INT4", "BOOL", "BOOL"}, [][]any{{"x", int64(8), true, true}}))
		run(referenceSelect("SELECT * FROM literal_source", []string{"a", "r", "active"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(3), true, true}}))
		run(tableLikeError("DROP TABLE renamed_anchor", referenceDiagnostic{
			Code: "2BP01", Message: "cannot drop table renamed_anchor because other objects depend on it",
			Detail: "column r of table literal_source depends on table renamed_anchor\n" +
				"constraint positive on table literal_source depends on table renamed_anchor\n" +
				"index literal_predicate depends on table renamed_anchor\n" +
				"column r of table literal_copy depends on table renamed_anchor\n" +
				"constraint positive on table literal_copy depends on table renamed_anchor\n" +
				"index literal_copy_a_idx depends on table renamed_anchor",
			Hint: "Use DROP ... CASCADE to drop the dependent objects too.",
		}))
		s.utility("DROP TABLE literal_copy", nil)
		s.utility("DROP TABLE literal_source", nil)
		s.utility("DROP TABLE renamed_anchor", nil)
	})
}
