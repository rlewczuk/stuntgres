package reference_test

import (
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRecursiveViewFeatureSmoke(t *testing.T) {
	recursiveViewReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE RECURSIVE VIEW rv_smoke(n) AS SELECT 1 UNION ALL SELECT n+1 FROM rv_smoke WHERE n<3", 0))
		s := recursiveViewRows("SELECT n FROM rv_smoke", "INT4", 1, 2, 3)
		s.CheckMetadata = recursiveViewMetadata(b, s.Columns, s.Types)
		run(s)
		run(fixture.Prepared(s))
		held := fixture.ViewHold(t, p, b, "SELECT n FROM rv_smoke WHERE n>=$1 ORDER BY n")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(held.SQL, []string{"n"}, []string{"INT4"}, recursiveViewInts(2, 3), 2))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(held.SQL, []string{"n"}, []string{"INT4"}, recursiveViewInts(3), 3))
		run(fixture.Exec("CREATE RECURSIVE VIEW rv_smoke_hidden(n) AS SELECT 1,0 AS depth UNION SELECT n,depth+1 FROM rv_smoke_hidden WHERE depth<2", 0))
		run(recursiveViewRows("SELECT n FROM rv_smoke_hidden", "INT4", 1, 1, 1))
		q := "CREATE RECURSIVE VIEW rv_smoke_bad(n) AS SELECT 1 UNION ALL SELECT n::bigint FROM rv_smoke_bad"
		want := fixture.ControlsReferenceDiagnostic("42804", `recursive query "rv_smoke_bad" column 1 has type integer in non-recursive term but type bigint overall`)
		want.Hint = "Cast the output of the non-recursive term to the correct type."
		want.Position = recursiveViewPosition(t, q, "1 UNION")
		recursiveViewAnalysisFailure(t, p, b, c, q, want, "execute")
		run(fixture.Exec("CREATE TABLE rv_smoke_edges(src INT,dst INT)", 0))
		run(fixture.Exec("INSERT INTO rv_smoke_edges VALUES(1,2),(2,3)", 2))
		run(fixture.Exec("CREATE RECURSIVE VIEW rv_smoke_path(n) AS SELECT 1 UNION SELECT dst FROM rv_smoke_edges e JOIN rv_smoke_path ON e.src=rv_smoke_path.n", 0))
		run(fixture.Exec("ALTER TABLE rv_smoke_edges RENAME COLUMN dst TO target", 0))
		run(fixture.Exec("INSERT INTO rv_smoke_edges VALUES(3,4)", 1))
		fixture.ViewCopy(t, p, b, "rv_smoke_path", "rv_smoke_copy")
		check := func() {
			run(recursiveViewRows("SELECT n FROM rv_smoke_copy", "INT4", 1, 2, 3, 4))
			run(recursiveViewRows("SELECT n FROM rv_smoke_hidden", "INT4", 1, 1, 1))
		}
		check()
		if !b.PG {
			dump := fixture.ViewDump(t, p)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			fixture.ViewRestore(t, p, dump)
			check()
		}
	})
}
