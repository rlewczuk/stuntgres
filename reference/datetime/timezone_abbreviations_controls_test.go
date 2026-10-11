package reference_test

import (
	"bytes"
	"context"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTimezoneAbbreviationsControlsCapture(t *testing.T) {
	p := fixture.TimezoneReferencePair(t)
	p.Step(t, fixture.Exec("SET timezone_abbreviations='India'", 0))
	p.Step(t, fixture.Exec("UPDATE tz_witness SET z='2004-07-01 12:00 IST',q='12:00 IST' WHERE id=1", 1))
	const query = "SELECT z::TEXT AS z,q::TEXT AS q FROM tz_witness"
	want := fixture.Select(query, []string{"z", "q"}, []string{"TEXT", "TEXT"}, [][]any{{"2004-07-01 06:30:00+00", "12:00:00+05:30"}})
	p.Step(t, want)
	conn, err := p.Memory.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	rows, err := conn.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, p.MemoryRecord), query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, p.MemoryRecord, s) }
	run(fixture.Exec("SET timezone_abbreviations='Australia'", 0))
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "timezone_abbreviations") || !strings.Contains(dump.String(), "2004-07-01 06:30:00+00") {
		t.Fatal(dump.String())
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	run(timezoneAbbreviationsShow("Australia"))
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	run(timezoneAbbreviationsShow("Australia"))
	run(want)
	fixture.RunStep(t, p.Ctx, timezoneAdmittedRows{Executor: conn, rows: rows}, p.MemoryRecord, want)
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal(err, redump.String())
	}
}
