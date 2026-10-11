package reference_test

import (
	"bytes"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceArithmeticRadixControls(t *testing.T) {
	p := fixture.NewScenario(t)
	const script = "CREATE TABLE n(v NUMERIC); INSERT INTO n VALUES(+0x8000000000000000)"
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	step := fixture.Select("SELECT v FROM n", []string{"v"}, []string{"NUMERIC"}, [][]any{{"9223372036854775808"}})
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, step)
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Prepared(step))
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
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, step)
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Prepared(step))
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatalf("radix redump %q: %v", redump.String(), err)
	}
	const bad = "CREATE TABLE d(f BOOL); INSERT INTO d VALUES(+0x8000000000000000)"
	want := fixture.ImplicitError(bad, fixture.Diagnostic{Code: "42804", Message: `column "f" is of type boolean but expression is of type numeric`, Hint: "You will need to rewrite or cast the expression.", Position: strconv.Itoa(strings.Index(bad, "+") + 1)})
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), want.Diagnostic)
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, step)
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Prepared(step))
}
