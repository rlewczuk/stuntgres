package reference_test

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"stuntgres"
)

func TestReferenceArithmeticRadixControls(t *testing.T) {
	p := newReferenceScenario(t)
	const script = "CREATE TABLE n(v NUMERIC); INSERT INTO n VALUES(+0x8000000000000000)"
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	step := referenceSelect("SELECT v FROM n", []string{"v"}, []string{"NUMERIC"}, [][]any{{"9223372036854775808"}})
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, step)
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referencePrepared(step))
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), "'9223372036854775808'") || strings.Contains(dump.String(), "0x8000000000000000") {
		t.Fatal("noncanonical radix dump", dump.String())
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, step)
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referencePrepared(step))
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatalf("radix redump %q: %v", redump.String(), err)
	}
	const bad = "CREATE TABLE d(f BOOL); INSERT INTO d VALUES(+0x8000000000000000)"
	want := referenceImplicitError(bad, referenceDiagnostic{Code: "42804", Message: `column "f" is of type boolean but expression is of type numeric`, Hint: "You will need to rewrite or cast the expression.", Position: strconv.Itoa(strings.Index(bad, "+") + 1)})
	referenceAssertDiagnostic(t, stuntgres.Restore(p.ctx, p.native, strings.NewReader(bad)), want.diagnostic)
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, step)
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referencePrepared(step))
}
