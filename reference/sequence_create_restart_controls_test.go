package reference_test

import (
	"bytes"
	"stuntgres"
	"testing"
)

func TestReferenceSequenceCreateRestartControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			p := newReferenceScenario(t)
			c := sequenceCoreConn(t, p.ctx, p.memory)
			exec := func(q string) {
				referenceRunStep(t, p.ctx, c, p.memoryRecord, referenceStep{query: q, prepared: prepared})
			}
			exec("CREATE SEQUENCE roundtrip START 5 RESTART WITH 9")
			sequenceRestartState(t, p, c, p.memoryRecord, false, "roundtrip", 9, false)
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			sequenceRestartState(t, p, c, p.memoryRecord, false, "roundtrip", 9, false)
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
				t.Fatal("roundtrip dump", err, redump.String())
			}
			sequenceRestartValue(t, p, c, p.memoryRecord, "SELECT nextval('roundtrip')", "nextval", 9)
			exec("ALTER SEQUENCE roundtrip RESTART")
			sequenceRestartState(t, p, c, p.memoryRecord, false, "roundtrip", 5, false)
			sequenceRestartValue(t, p, c, p.memoryRecord, "SELECT nextval('roundtrip')", "nextval", 5)
		})
	}
}
