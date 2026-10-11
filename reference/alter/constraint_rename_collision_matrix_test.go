package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceConstraintRenameCrossClassCollisions(t *testing.T) {
	destinations := []struct{ kind, declaration string }{
		{"n", "v INT CONSTRAINT occupied NOT NULL"},
		{"c", "v INT CONSTRAINT occupied CHECK(v>0)"},
		{"u", "v INT CONSTRAINT occupied UNIQUE"},
		{"p", "v INT CONSTRAINT occupied PRIMARY KEY"},
		{"x", "v INT,CONSTRAINT occupied EXCLUDE(v WITH =)"},
	}
	for _, source := range constraintRenameKinds {
		for _, dest := range destinations {
			// Two PRIMARY KEY definitions on one parent are illegal before rename.
			if source.kind == "p" && dest.kind == "p" {
				continue
			}
			t.Run(source.kind+"_to_"+dest.kind, func(t *testing.T) {
				constraintRenameScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t("+source.declaration+","+dest.declaration+")", 0))
					before := constraintRenameCapture(t, p, c, "old", source.indexed)
					code, message := "42710", `constraint "occupied" for relation "t" already exists`
					if source.indexed && (dest.kind == "u" || dest.kind == "p" || dest.kind == "x") {
						code, message = "42P07", `relation "occupied" already exists`
					}
					run(fixture.ErrorDiagnostic("ALTER TABLE t RENAME CONSTRAINT old TO occupied", constraintRenameError(code, message)))
					if constraintRenameCapture(t, p, c, "old", source.indexed) != before {
						t.Fatal("cross-class collision changed source")
					}
					run(fixture.Exec("ALTER TABLE t RENAME CONSTRAINT old TO new", 0))
					if constraintRenameCapture(t, p, c, "new", source.indexed) != before {
						t.Fatal("collision retry changed identity")
					}
					if source.kind == "p" {
						run(fixture.Select("SELECT conname,contype FROM pg_constraint WHERE conname='t_k_not_null'", []string{"conname", "contype"}, []string{"NAME", "CHAR"}, [][]any{{"t_k_not_null", "n"}}))
					}
				})
			})
		}
	}
	for _, kind := range []string{"n", "c"} {
		t.Run("other_table_"+kind, func(t *testing.T) {
			constraintRenameScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				declaration := "k INT CONSTRAINT old NOT NULL"
				other := "k INT CONSTRAINT shared NOT NULL"
				if kind == "c" {
					declaration = "k INT CONSTRAINT old CHECK(k>0)"
					other = "k INT CONSTRAINT shared CHECK(k>0)"
				}
				run(fixture.Exec("CREATE TABLE t("+declaration+")", 0))
				run(fixture.Exec("CREATE TABLE other("+other+")", 0))
				before := constraintRenameCapture(t, p, c, "old", false)
				run(fixture.Exec("ALTER TABLE t RENAME CONSTRAINT old TO shared", 0))
				if constraintRenameCapture(t, p, c, "shared", false) != before {
					t.Fatal("other table name changed identity")
				}
				run(fixture.Select("SELECT conname FROM pg_constraint WHERE conname='shared'", []string{"conname"}, []string{"NAME"}, [][]any{{"shared"}, {"shared"}}))
			})
		})
	}
}

func TestReferenceConstraintRenameExclusionDefinition(t *testing.T) {
	constraintRenameScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(k INT,payload TEXT,CONSTRAINT old EXCLUDE USING btree ((k+1) WITH =) WHERE(k>0) DEFERRABLE INITIALLY DEFERRED)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,'keep')", 1))
		before := constraintRenameCapture(t, p, c, "old", true)
		run(fixture.Exec("ALTER TABLE t RENAME CONSTRAINT old TO new", 0))
		if constraintRenameCapture(t, p, c, "new", true) != before {
			t.Fatal("exclusion definition/identity changed")
		}
		run(fixture.Exec("INSERT INTO t VALUES(0,'outside'),(0,'outside')", 2))
		run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1,'bad')", fixture.ExclusionReferenceConflict("t", "new", "(k + 1)", "2", "2")))
	})
}
