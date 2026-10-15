package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceConstraintRenameInheritanceBoundary(t *testing.T) {
	for _, kind := range []string{"c", "n"} {
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "direct/", true: "prepared/"}[prepared]+kind, func(t *testing.T) {
				constraintRenameScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					declaration := "k INT,CONSTRAINT old CHECK(k>0)"
					if kind == "n" {
						declaration = "k INT CONSTRAINT old NOT NULL"
					}
					run(fixture.Exec("CREATE TABLE t("+declaration+")", 0))
					run(fixture.Exec("CREATE TABLE child() INHERITS(t)", 0))
					before := constraintRenameCapture(t, p, c, "old", false)
					for _, tc := range []struct{ q, message string }{
						{"ALTER TABLE child RENAME CONSTRAINT old TO new", `cannot rename inherited constraint "old"`},
						{"ALTER TABLE ONLY t RENAME CONSTRAINT old TO new", `inherited constraint "old" must be renamed in child tables too`},
					} {
						d := constraintRenameError("42P16", tc.message)
						run(fixture.ErrorDiagnostic(tc.q, d))
						if constraintRenameCapture(t, p, c, "old", false) != before {
							t.Fatal("graph failure changed constraint")
						}
					}
				})
			})
		}
	}
	for _, k := range constraintRenameKinds {
		if !k.indexed {
			continue
		}
		t.Run("local_"+k.kind, func(t *testing.T) {
			constraintRenameScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE parent(extra INT)", 0))
				run(fixture.Exec("CREATE TABLE t("+k.declaration+") INHERITS(parent)", 0))
				before := constraintRenameCapture(t, p, c, "old", true)
				run(fixture.Exec("ALTER TABLE t RENAME CONSTRAINT old TO new", 0))
				if constraintRenameCapture(t, p, c, "new", true) != before {
					t.Fatal("local backing group changed identity")
				}
				run(fixture.Exec("INSERT INTO t(extra,k,payload) VALUES(7,1,'keep')", 1))
				if k.kind == "x" {
					run(fixture.ErrorDiagnostic("INSERT INTO t(extra,k,payload) VALUES(7,1,'bad')", fixture.ExclusionReferenceConflict("t", "new", "k", "1", "1")))
				} else {
					run(fixture.ErrorDiagnostic("INSERT INTO t(extra,k,payload) VALUES(7,1,'bad')", fixture.UniqueReferenceDuplicate("t", "new", "Key (k)=(1) already exists.")))
				}
			})
		})
	}
}
