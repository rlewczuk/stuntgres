package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceNumericLiteralsControlsAndCancellation(t *testing.T) {
	p := newReferenceScenario(t)
	script := "CREATE TABLE t(id INT,b BIGINT); INSERT INTO t VALUES (1,0b1010),(2,1_000),(3,0x7FFF_FFFF_FFFF_FFFF),(4,-0x8000_0000_0000_0000)"
	s := referenceSelect("SELECT id,b FROM t ORDER BY id", []string{"id", "b"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(1000)}, {int64(3), int64(9223372036854775807)}, {int64(4), int64(-9223372036854775808)}})
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	if p.postgres != nil {
		if _, err := p.postgres.ExecContext(p.ctx, script); err != nil {
			t.Fatal(err)
		}
	}
	p.step(t, s)
	held, err := p.native.QueryContext(p.ctx, s.query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	bad := "CREATE TABLE staged(x INT); INSERT INTO staged VALUES (1); INSERT INTO staged VALUES (0b2)"
	want := controlsReferenceDiagnostic("42601", `trailing junk after numeric literal at or near "0b2"`)
	want.Position = "87"
	referenceAssertDiagnostic(t, stuntgres.Restore(p.ctx, p.native, strings.NewReader(bad)), &want)
	if p.postgres != nil {
		step := referenceErrorDiagnostic(bad, want)
		step.errorStage = "execute"
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, step)
	}
	p.step(t, s)
	for _, backend := range p.backends() {
		step := referenceError("SELECT x FROM staged", "42P01")
		step.selectRows = true
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
	}
	lexicalRows(t, false, held, s, nil)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
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
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	p.step(t, s)
	canceled, cancel := context.WithCancel(p.ctx)
	cancel()
	for _, backend := range p.backends() {
		_, err := backend.db.ExecContext(canceled, "INSERT INTO t VALUES (5,0xa)")
		lexicalOrdinary(t, err, context.Canceled)
		stmt, err := backend.db.PrepareContext(canceled, "INSERT INTO t VALUES (5,0xa)")
		if stmt != nil {
			stmt.Close()
			t.Fatal("canceled Prepare succeeded")
		}
		lexicalOrdinary(t, err, context.Canceled)
	}
	lexicalOrdinary(t, stuntgres.Restore(canceled, p.native, strings.NewReader(script)), context.Canceled)
	failure := errors.New("numeric control I/O")
	lexicalOrdinary(t, stuntgres.Restore(p.ctx, p.native, lexicalFailIO{failure}), failure)
	lexicalOrdinary(t, stuntgres.Dump(p.ctx, p.native, lexicalFailIO{failure}), failure)
	p.step(t, s)
	for _, backend := range p.backends() {
		rows, err := backend.db.QueryContext(p.ctx, s.query)
		if err != nil {
			t.Fatal(err)
		}
		lexicalRows(t, backend.pg, rows, s, nil)
	}
}
