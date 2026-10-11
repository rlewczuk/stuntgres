package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTruncateHierarchyOnly(t *testing.T) {
	truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
		truncateExec(t, f, f.A, "CREATE TABLE parent(id INT PRIMARY KEY)", 0)
		truncateExec(t, f, f.A, "CREATE TABLE child() INHERITS(parent)", 0)
		truncateExec(t, f, f.A, "INSERT INTO parent VALUES(1)", 1)
		truncateExec(t, f, f.A, "INSERT INTO child VALUES(2)", 1)
		truncateExec(t, f, f.A, "TRUNCATE ONLY parent", 0)
		truncateRows(t, f, f.A, "child", 2)
		// The parent PK is physical and not inherited: reinserting its former key
		// proves ONLY emptied the parent without requiring recursive SELECT support.
		truncateExec(t, f, f.A, "INSERT INTO parent VALUES(1)", 1)
		if b.PG {
			truncateRun(t, f, f.A, fixture.Select("SELECT id FROM ONLY parent ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		}
		truncateExec(t, f, f.A, "TRUNCATE parent", 0)
		truncateRows(t, f, f.A, "child")
		truncateExec(t, f, f.A, "INSERT INTO parent VALUES(1)", 1)
		truncateExec(t, f, f.A, "INSERT INTO child VALUES(3)", 1)
		truncateExec(t, f, f.A, "TRUNCATE parent *", 0)
		truncateRows(t, f, f.A, "child")
		truncateExec(t, f, f.A, "INSERT INTO parent VALUES(1)", 1)
	})
}

func TestReferenceTruncateHierarchyTemporarySessions(t *testing.T) {
	truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
		truncateExec(t, f, f.A, "CREATE TABLE p(id INT)", 0)
		truncateExec(t, f, f.A, "CREATE TEMP TABLE own_child() INHERITS(p)", 0)
		truncateExec(t, f, f.B, "CREATE TEMP TABLE peer_child() INHERITS(p)", 0)
		truncateExec(t, f, f.A, "INSERT INTO p VALUES(1)", 1)
		truncateExec(t, f, f.A, "INSERT INTO own_child VALUES(2)", 1)
		truncateExec(t, f, f.B, "INSERT INTO peer_child VALUES(3)", 1)
		truncateRows(t, f, f.A, "p", 1, 2)
		truncateRows(t, f, f.B, "p", 1, 3)
		truncateExec(t, f, f.A, "TRUNCATE p", 0)
		truncateRows(t, f, f.A, "p")
		truncateRows(t, f, f.A, "own_child")
		truncateRows(t, f, f.B, "peer_child", 3)
		truncateRows(t, f, f.B, "p", 3)
		// Reverse the invoking session: its temporary child now participates,
		// while the first session's reinserted temporary row remains private.
		truncateExec(t, f, f.A, "INSERT INTO p VALUES(1)", 1)
		truncateExec(t, f, f.A, "INSERT INTO own_child VALUES(2)", 1)
		truncateExec(t, f, f.B, "TRUNCATE p *", 0)
		truncateRows(t, f, f.B, "p")
		truncateRows(t, f, f.B, "peer_child")
		truncateRows(t, f, f.A, "own_child", 2)
		truncateRows(t, f, f.A, "p", 2)
	})
}
