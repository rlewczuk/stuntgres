package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceForeignActionsDefaultsPlanning(t *testing.T) {
	foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE c(pid INT DEFAULT 1/0 REFERENCES p ON DELETE SET DEFAULT)", 0))
		run(fixture.Exec("INSERT INTO p VALUES(2)", 1))
		// ri_set prepares its generated UPDATE before scanning for child rows.
		// Constant folding can therefore fail even when the child is empty;
		// Prepare of the initiating DELETE still performs no default evaluation.
		d := foreignActionsContext(foreignKeyError("22012", "division by zero", "", "", ""), foreignActionsSQL("c", "pid", "SET DEFAULT", "DELETE"))
		for _, prepared := range []bool{false, true} {
			run(foreignKeyFail("DELETE FROM p", d, prepared))
		}
		run(foreignKeyIntRows("SELECT id FROM p", "id", 2))
		run(foreignKeyIntRows("SELECT pid FROM c", "pid"))
	})
}

func TestReferenceForeignActionsDefaultsSubsetDemand(t *testing.T) {
	foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE p(a INT,b INT,PRIMARY KEY(a,b))", 0))
		run(fixture.Exec("CREATE TABLE c(a INT DEFAULT 1/0,b INT DEFAULT 1,CONSTRAINT fk FOREIGN KEY(a,b) REFERENCES p ON DELETE SET DEFAULT(b))", 0))
		run(fixture.Exec("INSERT INTO p VALUES(7,1),(7,2)", 2))
		run(fixture.Exec("INSERT INTO c VALUES(7,2)", 1))
		run(fixture.Prepared(fixture.Exec("DELETE FROM p WHERE b=2", 1)))
		run(fixture.Select("SELECT a,b FROM c", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(1)}}))
		run(fixture.Select("SELECT a,b FROM p", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(1)}}))
	})
}

func TestReferenceForeignActionsConstraintsTypmod(t *testing.T) {
	foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE p(id TEXT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE c(pid VARCHAR(2) CONSTRAINT fk REFERENCES p ON UPDATE CASCADE,note TEXT)", 0))
		run(fixture.Exec("INSERT INTO p VALUES('ok')", 1))
		run(fixture.Exec("INSERT INTO c VALUES('ok','kept')", 1))
		d := foreignActionsContext(foreignKeyError("22001", "value too long for type character varying(2)", "", "", ""), `UPDATE ONLY "public"."c" SET "pid" = $1 WHERE $2 OPERATOR(pg_catalog.=) "pid"::pg_catalog.text`)
		for _, prepared := range []bool{false, true} {
			run(foreignKeyFail("UPDATE p SET id='long'", d, prepared))
		}
		run(fixture.Select("SELECT id FROM p", []string{"id"}, []string{"TEXT"}, [][]any{{"ok"}}))
		run(fixture.Select("SELECT pid,note FROM c", []string{"pid", "note"}, []string{"VARCHAR", "TEXT"}, [][]any{{"ok", "kept"}}))
	})
}
