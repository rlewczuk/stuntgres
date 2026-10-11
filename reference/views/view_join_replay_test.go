package reference_test

import (
	"bytes"
	"context"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewJoinedReplayAtomicityAndDynamicConsumers(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, s := range referenceViewJoinSeed(true) {
				run(s)
			}
			for _, name := range referenceViewJoinNames(true) {
				s := referenceViewJoinRead(name, "", true)
				run(s)
				run(fixture.Prepared(s))
				fixture.ViewCopy(t, p, b, name, name+"_copy")
				s = referenceViewJoinRead(name, "_copy", true)
				run(s)
				run(fixture.Prepared(s))
			}
			if b.PG {
				run(fixture.Exec("INSERT INTO renamed_a VALUES(0,4,'d','2000-02-01')", 1))
				run(fixture.Exec("INSERT INTO renamed_b VALUES(4,'w')", 1))
				run(fixture.Select("SELECT id,x,y FROM nested WHERE id=4", []string{"id", "x", "y"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(4), "d", "w"}}))
			}
		})
	}
	dump := fixture.ViewDump(t, p)
	t.Run("native_destination", func(t *testing.T) {
		destination := fixture.NewFixture(t, false, true)
		run := func(s fixture.Step) {
			fixture.RunStep(t, destination.Ctx, destination.Memory, destination.MemoryRecord, s)
		}
		for _, s := range []fixture.Step{fixture.Exec("CREATE TEMPORARY SEQUENCE retained", 0), fixture.Exec("CREATE TABLE sentinel(id INT)", 0), fixture.Exec("INSERT INTO sentinel VALUES(17)", 1)} {
			run(s)
		}
		sentinel := fixture.Select("SELECT id FROM sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(17)}})
		invalid := append(append([]byte(nil), dump...), []byte("CREATE VIEW invalid AS SELECT missing FROM renamed_a;\n")...)
		if err := stuntgres.Restore(destination.Ctx, destination.Native, bytes.NewReader(invalid)); err == nil {
			t.Fatal("invalid suffix restored")
		}
		run(sentinel)
		canceled, cancel := context.WithCancel(destination.Ctx)
		cancel()
		if err := stuntgres.Restore(canceled, destination.Native, bytes.NewReader(dump)); err == nil {
			t.Fatal("canceled restore succeeded")
		}
		run(sentinel)
		fixture.ViewRestore(t, destination, dump)
		for _, name := range referenceViewJoinNames(true) {
			for _, suffix := range []string{"", "_copy"} {
				s := referenceViewJoinRead(name, suffix, true)
				run(s)
				run(fixture.Prepared(s))
			}
		}
		run(fixture.Exec("INSERT INTO renamed_a VALUES(0,4,'d','2000-02-01')", 1))
		run(fixture.Exec("INSERT INTO renamed_b VALUES(4,'w')", 1))
		run(fixture.Select("SELECT id,x,y FROM nested WHERE id=4", []string{"id", "x", "y"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(4), "d", "w"}}))
	})
}
