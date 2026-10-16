package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantStoredRecipeReplacement(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("CREATE TABLE body_defaults(v integer DEFAULT bit_length('abc'))", 0))
		run(fixture.Exec("CREATE VIEW body_view AS SELECT bit_length('abc') AS v", 0))
		// The SQL body is shared: three ASCII octets have 24 bits, each DEFAULT
		// insertion adds exactly one occurrence, and the view always has one row.
		check := func(run func(fixture.Step), tableRows int) {
			run(fixture.Exec("INSERT INTO body_defaults DEFAULT VALUES", 1))
			rows := [][]any{{int64(24)}}
			if tableRows == 2 {
				rows = append(rows, []any{int64(24)})
			}
			table := fixture.Select("SELECT v FROM body_defaults", []string{"v"}, []string{"INT4"}, rows)
			table.Bag = true
			run(table)
			run(fixture.Select("SELECT v FROM body_view", []string{"v"}, []string{"INT4"}, [][]any{{int64(24)}}))
		}
		if b.PG {
			// PostgreSQL supplies independent ordinary SQL outcomes, not a fake
			// implementation of native catalog replacement or native controls.
			check(run, 1)
			check(run, 2)
			return
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		var originalDump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &originalDump); err != nil {
			t.Fatal("source recipes must serialize", err)
		}
		// These extra fixture-owned native engines are public clone/isolation
		// witnesses only. They never start or reset another PostgreSQL oracle.
		target := fixture.NewFixture(t, false, true)
		if err := stuntgres.Restore(p.Ctx, target.Native, bytes.NewReader(originalDump.Bytes())); err != nil {
			t.Fatal("import source recipes into an independent engine", err)
		}
		runner := func(pair *fixture.Pair) func(fixture.Step) {
			return func(s fixture.Step) {
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, pair.Memory, pair.Backends()[0].Recorder, s)
			}
		}
		check(runner(target), 1)
		var targetDump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, target.Native, &targetDump); err != nil {
			t.Fatal("imported original body lifetime must serialize", err)
		}
		replayed := fixture.NewFixture(t, false, true)
		if err := stuntgres.Restore(p.Ctx, replayed.Native, bytes.NewReader(targetDump.Bytes())); err != nil {
			t.Fatal("replay populated recipes", err)
		}
		check(runner(replayed), 2)
		var unchanged bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &unchanged); err != nil {
			t.Fatal("redump original source", err)
		}
		if !bytes.Equal(originalDump.Bytes(), unchanged.Bytes()) {
			t.Fatal("destination insertion/replay changed original source dump")
		}
	})
}
