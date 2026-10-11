package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequenceCreateRestartControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			c := fixture.SequenceCoreConn(t, p.Ctx, p.Memory)
			exec := func(q string) {
				fixture.RunStep(t, p.Ctx, c, p.MemoryRecord, fixture.Step{Query: q, Prepared: prepared})
			}
			exec("CREATE SEQUENCE roundtrip START 5 RESTART WITH 9")
			sequenceRestartState(t, p, c, p.MemoryRecord, false, "roundtrip", 9, false)
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			sequenceRestartState(t, p, c, p.MemoryRecord, false, "roundtrip", 9, false)
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
				t.Fatal("roundtrip dump", err, redump.String())
			}
			sequenceRestartValue(t, p, c, p.MemoryRecord, "SELECT nextval('roundtrip')", "nextval", 9)
			exec("ALTER SEQUENCE roundtrip RESTART")
			sequenceRestartState(t, p, c, p.MemoryRecord, false, "roundtrip", 5, false)
			sequenceRestartValue(t, p, c, p.MemoryRecord, "SELECT nextval('roundtrip')", "nextval", 5)
		})
	}
}
