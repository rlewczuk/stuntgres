package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRecursiveViewDependencies(t *testing.T) {
	recursiveViewReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE rv_edges(src INT,dst INT)", 0),
			fixture.Exec("INSERT INTO rv_edges VALUES(1,2),(2,3)", 2),
			fixture.Exec("CREATE RECURSIVE VIEW rv_path(n) AS SELECT 1 UNION SELECT e.dst FROM rv_edges e JOIN rv_path ON e.src=rv_path.n", 0),
		} {
			run(s)
		}
		held := fixture.ViewHold(t, p, b, "SELECT n FROM rv_path")
		check := func(values ...int64) {
			s := recursiveViewRows(held.SQL, "INT4", values...)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			run(s)
			run(fixture.Prepared(s))
		}
		check(1, 2, 3)
		for _, s := range []fixture.Step{
			fixture.Exec("ALTER TABLE rv_edges RENAME TO rv_renamed_edges", 0),
			fixture.Exec("ALTER TABLE rv_renamed_edges RENAME COLUMN dst TO target", 0),
			fixture.Exec("CREATE TABLE rv_edges(src INT,dst INT)", 0),
			fixture.Exec("INSERT INTO rv_edges VALUES(1,99)", 1),
			fixture.Exec("INSERT INTO rv_renamed_edges VALUES(3,4)", 1),
		} {
			run(s)
		}
		check(1, 2, 3, 4)
		want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop column target of table rv_renamed_edges because other objects depend on it")
		want.Detail = "view rv_path depends on column target of table rv_renamed_edges"
		want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("ALTER TABLE rv_renamed_edges DROP COLUMN target", want)
			s.Prepared = prepared
			run(s)
		}
		check(1, 2, 3, 4)
		run(fixture.Exec("CREATE TABLE rv_hidden_base(seed INT,depth INT)", 0))
		run(fixture.Exec("INSERT INTO rv_hidden_base VALUES(1,0)", 1))
		run(fixture.Exec("CREATE RECURSIVE VIEW rv_hidden(n) AS SELECT seed,depth FROM rv_hidden_base UNION SELECT n,depth+1 FROM rv_hidden WHERE depth<2", 0))
		run(recursiveViewRows("SELECT n FROM rv_hidden", "INT4", 1, 1, 1))
		want = fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop column depth of table rv_hidden_base because other objects depend on it")
		want.Detail = "view rv_hidden depends on column depth of table rv_hidden_base"
		want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		run(fixture.ErrorDiagnostic("ALTER TABLE rv_hidden_base DROP COLUMN depth", want))
		run(recursiveViewRows("SELECT n FROM rv_hidden", "INT4", 1, 1, 1))
	})
}
