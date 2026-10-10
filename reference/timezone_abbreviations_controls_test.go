package reference_test

import (
	"bytes"
	"context"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceTimezoneAbbreviationsControlsCapture(t *testing.T) {
	p := timezoneReferencePair(t)
	p.step(t, referenceExec("SET timezone_abbreviations='India'", 0))
	p.step(t, referenceExec("UPDATE tz_witness SET z='2004-07-01 12:00 IST',q='12:00 IST' WHERE id=1", 1))
	const query = "SELECT z::TEXT AS z,q::TEXT AS q FROM tz_witness"
	want := referenceSelect(query, []string{"z", "q"}, []string{"TEXT", "TEXT"}, [][]any{{"2004-07-01 06:30:00+00", "12:00:00+05:30"}})
	p.step(t, want)
	conn, err := p.memory.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	rows, err := conn.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, p.memoryRecord), query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, p.memoryRecord, s) }
	run(referenceExec("SET timezone_abbreviations='Australia'", 0))
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "timezone_abbreviations") || !strings.Contains(dump.String(), "2004-07-01 06:30:00+00") {
		t.Fatal(dump.String())
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	run(timezoneAbbreviationsShow("Australia"))
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	run(timezoneAbbreviationsShow("Australia"))
	run(want)
	referenceRunStep(t, p.ctx, timezoneAdmittedRows{referenceExecutor: conn, rows: rows}, p.memoryRecord, want)
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal(err, redump.String())
	}
}
