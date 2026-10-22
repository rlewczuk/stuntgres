package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRecursiveViewReplay(t *testing.T) {
	recursiveViewReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		db, recorder := b.DB, b.Recorder
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, db, recorder, s) }
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE rv_replay_edges(src INT,dst INT)", 0),
			fixture.Exec("INSERT INTO rv_replay_edges VALUES(1,2),(2,3)", 2),
			fixture.Exec("CREATE RECURSIVE VIEW rv_replay(n) AS SELECT 1 UNION SELECT dst FROM rv_replay_edges e JOIN rv_replay ON e.src=rv_replay.n", 0),
			fixture.Exec("CREATE RECURSIVE VIEW rv_replay_hidden(n) AS SELECT 1,0 AS depth UNION SELECT n,depth+1 FROM rv_replay_hidden WHERE depth<2", 0),
			fixture.Exec("CREATE VIEW rv_replay_nested AS SELECT n FROM rv_replay", 0),
			fixture.Exec("ALTER VIEW rv_replay RENAME TO rv_replay_renamed", 0),
		} {
			run(s)
		}
		fixture.ViewCopy(t, p, b, "rv_replay_renamed", "rv_replay_copy")
		check := func(values ...int64) {
			for _, name := range []string{"rv_replay_renamed", "rv_replay_nested", "rv_replay_copy"} {
				s := recursiveViewRows("SELECT n FROM "+name, "INT4", values...)
				s.CheckMetadata = recursiveViewMetadata(b, s.Columns, s.Types)
				run(s)
				run(fixture.Prepared(s))
			}
			run(recursiveViewRows("SELECT n FROM rv_replay_hidden", "INT4", 1, 1, 1))
		}
		check(1, 2, 3)
		if !b.PG {
			dump := fixture.ViewDump(t, p)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			fixture.ViewRestore(t, p, dump)
			check(1, 2, 3)
			native := p.Native
			for range 2 {
				var out bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, native, &out); err != nil {
					t.Fatal(err)
				}
				destination := fixture.NewFixture(t, false, true)
				destination.Memory.SetMaxOpenConns(1)
				for _, q := range []string{"CREATE TEMP SEQUENCE rv_retained_one", "CREATE TEMP SEQUENCE rv_retained_two"} {
					fixture.RunStep(t, destination.Ctx, destination.Memory, destination.MemoryRecord, fixture.Exec(q, 0))
				}
				if err := stuntgres.Restore(p.Ctx, destination.Native, bytes.NewReader(out.Bytes())); err != nil {
					t.Fatal(err)
				}
				native, db, recorder = destination.Native, destination.Memory, destination.MemoryRecord
				check(1, 2, 3)
			}
		}
		run(fixture.Exec("INSERT INTO rv_replay_edges VALUES(3,4)", 1))
		check(1, 2, 3, 4)
		run(fixture.Exec("DROP VIEW rv_replay_nested", 0))
		run(fixture.Exec("DROP VIEW rv_replay_copy", 0))
		want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop column dst of table rv_replay_edges because other objects depend on it")
		want.Detail = "view rv_replay_renamed depends on column dst of table rv_replay_edges"
		want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		run(fixture.ErrorDiagnostic("ALTER TABLE rv_replay_edges DROP COLUMN dst", want))
	})
}
