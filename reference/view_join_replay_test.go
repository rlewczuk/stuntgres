package reference_test

import (
	"bytes"
	"context"
	"stuntgres"
	"testing"
)

func TestReferenceViewJoinedReplayAtomicityAndDynamicConsumers(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			for _, s := range referenceViewJoinSeed(true) {
				run(s)
			}
			for _, name := range referenceViewJoinNames(true) {
				s := referenceViewJoinRead(name, "", true)
				run(s)
				run(referencePrepared(s))
				referenceViewCopy(t, p, b, name, name+"_copy")
				s = referenceViewJoinRead(name, "_copy", true)
				run(s)
				run(referencePrepared(s))
			}
			if b.pg {
				run(referenceExec("INSERT INTO renamed_a VALUES(0,4,'d','2000-02-01')", 1))
				run(referenceExec("INSERT INTO renamed_b VALUES(4,'w')", 1))
				run(referenceSelect("SELECT id,x,y FROM nested WHERE id=4", []string{"id", "x", "y"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(4), "d", "w"}}))
			}
		})
	}
	dump := referenceViewDump(t, p)
	t.Run("native_destination", func(t *testing.T) {
		destination := newReferenceFixture(t, false, true)
		run := func(s referenceStep) {
			referenceRunStep(t, destination.ctx, destination.memory, destination.memoryRecord, s)
		}
		for _, s := range []referenceStep{referenceExec("CREATE TEMPORARY SEQUENCE retained", 0), referenceExec("CREATE TABLE sentinel(id INT)", 0), referenceExec("INSERT INTO sentinel VALUES(17)", 1)} {
			run(s)
		}
		sentinel := referenceSelect("SELECT id FROM sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(17)}})
		invalid := append(append([]byte(nil), dump...), []byte("CREATE VIEW invalid AS SELECT missing FROM renamed_a;\n")...)
		if err := stuntgres.Restore(destination.ctx, destination.native, bytes.NewReader(invalid)); err == nil {
			t.Fatal("invalid suffix restored")
		}
		run(sentinel)
		canceled, cancel := context.WithCancel(destination.ctx)
		cancel()
		if err := stuntgres.Restore(canceled, destination.native, bytes.NewReader(dump)); err == nil {
			t.Fatal("canceled restore succeeded")
		}
		run(sentinel)
		referenceViewRestore(t, destination, dump)
		for _, name := range referenceViewJoinNames(true) {
			for _, suffix := range []string{"", "_copy"} {
				s := referenceViewJoinRead(name, suffix, true)
				run(s)
				run(referencePrepared(s))
			}
		}
		run(referenceExec("INSERT INTO renamed_a VALUES(0,4,'d','2000-02-01')", 1))
		run(referenceExec("INSERT INTO renamed_b VALUES(4,'w')", 1))
		run(referenceSelect("SELECT id,x,y FROM nested WHERE id=4", []string{"id", "x", "y"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(4), "d", "w"}}))
	})
}
