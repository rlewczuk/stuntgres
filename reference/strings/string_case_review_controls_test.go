package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringCaseReviewStoredConflict(t *testing.T) {
	stringCaseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, op := range []string{"lower", "upper", "casefold", "initcap"} {
			run(fixture.Exec(`CREATE TABLE sc_review_conflict_`+op+`(id INT,a TEXT COLLATE "C",b TEXT COLLATE "POSIX",CONSTRAINT sc_review_conflict_check_`+op+` CHECK (`+op+`(a||b)='ab'))`, 0))
		}
		assertConflict := func() {
			for _, op := range []string{"lower", "upper", "casefold", "initcap"} {
				name := op
				if op == "casefold" {
					name = "lower"
				}
				d := fixture.ControlsReferenceDiagnostic("42P22", "could not determine which collation to use for "+name+"() function")
				d.Hint = "Use the COLLATE clause to set the collation explicitly."
				for _, prepared := range []bool{false, true} {
					s := fixture.Exec("INSERT INTO sc_review_conflict_"+op+" VALUES(1,NULL,'B')", 1)
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
					s = fixture.ErrorDiagnostic("INSERT INTO sc_review_conflict_"+op+" VALUES(2,'A','B')", d)
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
				}
				run(fixture.Select("SELECT id,a,b FROM sc_review_conflict_"+op+" ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), nil, "B"}, {int64(1), nil, "B"}}))
				run(fixture.Exec("DELETE FROM sc_review_conflict_"+op, 2))
			}
		}
		assertConflict()
		if !b.PG {
			// Public replacement controls are native-only; SQL expectations above
			// are the same independently checked contract before and after replace.
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			assertConflict()
		}
	})
}
