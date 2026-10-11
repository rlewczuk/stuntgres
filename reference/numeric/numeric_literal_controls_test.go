package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceNumericLiteralsControlsAndCancellation(t *testing.T) {
	p := fixture.NewScenario(t)
	script := "CREATE TABLE t(id INT,b BIGINT); INSERT INTO t VALUES (1,0b1010),(2,1_000),(3,0x7FFF_FFFF_FFFF_FFFF),(4,-0x8000_0000_0000_0000)"
	s := fixture.Select("SELECT id,b FROM t ORDER BY id", []string{"id", "b"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(1000)}, {int64(3), int64(9223372036854775807)}, {int64(4), int64(-9223372036854775808)}})
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, script); err != nil {
			t.Fatal(err)
		}
	}
	p.Step(t, s)
	held, err := p.Native.QueryContext(p.Ctx, s.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	bad := "CREATE TABLE staged(x INT); INSERT INTO staged VALUES (1); INSERT INTO staged VALUES (0b2)"
	want := fixture.ControlsReferenceDiagnostic("42601", `trailing junk after numeric literal at or near "0b2"`)
	want.Position = "87"
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &want)
	if p.Postgres != nil {
		step := fixture.ErrorDiagnostic(bad, want)
		step.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, step)
	}
	p.Step(t, s)
	for _, backend := range p.Backends() {
		step := fixture.Error("SELECT x FROM staged", "42P01")
		step.SelectRows = true
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
	}
	fixture.LexicalRows(t, false, held, s, nil)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, spelling := range []string{"10", "1000", "9223372036854775807", "-9223372036854775808"} {
		if !strings.Contains(dump.String(), spelling) {
			t.Fatalf("canonical dump missing %s: %s", spelling, dump.String())
		}
	}
	for _, spelling := range []string{"0b", "0x", "1_000", "7FFF"} {
		if strings.Contains(dump.String(), spelling) {
			t.Fatalf("noncanonical dump: %s", dump.String())
		}
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	p.Step(t, s)
	canceled, cancel := context.WithCancel(p.Ctx)
	cancel()
	for _, backend := range p.Backends() {
		_, err := backend.DB.ExecContext(canceled, "INSERT INTO t VALUES (5,0xa)")
		fixture.LexicalOrdinary(t, err, context.Canceled)
		stmt, err := backend.DB.PrepareContext(canceled, "INSERT INTO t VALUES (5,0xa)")
		if stmt != nil {
			stmt.Close()
			t.Fatal("canceled Prepare succeeded")
		}
		fixture.LexicalOrdinary(t, err, context.Canceled)
	}
	fixture.LexicalOrdinary(t, stuntgres.Restore(canceled, p.Native, strings.NewReader(script)), context.Canceled)
	failure := errors.New("numeric control I/O")
	fixture.LexicalOrdinary(t, stuntgres.Restore(p.Ctx, p.Native, fixture.LexicalFailIO{failure}), failure)
	fixture.LexicalOrdinary(t, stuntgres.Dump(p.Ctx, p.Native, fixture.LexicalFailIO{failure}), failure)
	p.Step(t, s)
	for _, backend := range p.Backends() {
		rows, err := backend.DB.QueryContext(p.Ctx, s.Query)
		if err != nil {
			t.Fatal(err)
		}
		fixture.LexicalRows(t, backend.PG, rows, s, nil)
	}
}
