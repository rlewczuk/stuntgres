package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
)

func TestReferenceStringCaseReviewStoredConflict(t *testing.T) {
	stringCaseScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		for _, op := range []string{"lower", "upper", "casefold", "initcap"} {
			run(referenceExec(`CREATE TABLE sc_review_conflict_`+op+`(id INT,a TEXT COLLATE "C",b TEXT COLLATE "POSIX",CONSTRAINT sc_review_conflict_check_`+op+` CHECK (`+op+`(a||b)='ab'))`, 0))
		}
		assertConflict := func() {
			for _, op := range []string{"lower", "upper", "casefold", "initcap"} {
				name := op
				if op == "casefold" {
					name = "lower"
				}
				d := controlsReferenceDiagnostic("42P22", "could not determine which collation to use for "+name+"() function")
				d.Hint = "Use the COLLATE clause to set the collation explicitly."
				for _, prepared := range []bool{false, true} {
					s := referenceExec("INSERT INTO sc_review_conflict_"+op+" VALUES(1,NULL,'B')", 1)
					if prepared {
						s = referencePrepared(s)
					}
					run(s)
					s = referenceErrorDiagnostic("INSERT INTO sc_review_conflict_"+op+" VALUES(2,'A','B')", d)
					s.errorStage = "execute"
					if prepared {
						s = referencePrepared(s)
					}
					run(s)
				}
				run(referenceSelect("SELECT id,a,b FROM sc_review_conflict_"+op+" ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), nil, "B"}, {int64(1), nil, "B"}}))
				run(referenceExec("DELETE FROM sc_review_conflict_"+op, 2))
			}
		}
		assertConflict()
		if !b.pg {
			// Public replacement controls are native-only; SQL expectations above
			// are the same independently checked contract before and after replace.
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			assertConflict()
		}
	})
}
