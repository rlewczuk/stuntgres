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

func TestReferenceTimezoneWideOffsetNativeRoundTrip(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		backend.DB.SetMaxOpenConns(1)
		backend.DB.SetMaxIdleConns(1)
	}
	p.Step(t, fixture.Exec("SET TIME ZONE 'UTC'", 0))
	p.Step(t, fixture.Exec("CREATE TABLE tz_wide(id INTEGER,z TIMESTAMPTZ,q TIMETZ)", 0))
	p.Step(t, fixture.Exec("INSERT INTO tz_wide VALUES(1,'-infinity','00:00+15:59:59'),(2,'infinity','24:00-15:59:59'),(3,'epoch',NULL)", 3))
	timezoneBoth(t, p, fixture.Select("SELECT z::TEXT AS z FROM tz_wide ORDER BY z", []string{"z"}, []string{"TEXT"}, [][]any{{"-infinity"}, {"1970-01-01 00:00:00+00"}, {"infinity"}}))
	timezoneBoth(t, p, fixture.Select("SELECT TIMESTAMPTZ '2004-01-01 24:00+00'::TEXT AS a,TIMESTAMPTZ '2004-01-01 00:00+15:59:59'::TEXT AS b FROM tz_wide WHERE id=1", []string{"a", "b"}, []string{"TEXT", "TEXT"}, [][]any{{"2004-01-02 00:00:00+00", "2003-12-31 08:00:01+00"}}))
	for _, backend := range p.Backends() {
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("SET TIME ZONE 'UTC167:59:60'", 0))
		query := "SELECT (TIMETZ '12:00')::TEXT AS q FROM tz_wide WHERE id=1"
		held := timezoneHeld(t, p, backend.DB, query)
		var rawHeld fixture.HeldStatement
		rawQuery := "SELECT TIMETZ '12:00' AS q FROM tz_wide WHERE id=1"
		if !backend.PG {
			rawHeld = timezoneHeld(t, p, backend.DB, rawQuery)
		}
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO tz_wide VALUES(4,NULL,'12:00')", 1))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("SET TIME ZONE 'UTC'", 0))
		fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Select(query, []string{"q"}, []string{"TEXT"}, [][]any{{"12:00:00-168"}}))
		if !backend.PG {
			fixture.RunStep(t, p.Ctx, rawHeld, backend.Recorder, fixture.Select(rawQuery, []string{"q"}, []string{"TIMETZ"}, [][]any{{"12:00:00-168"}}))
		}
	}
	p.Step(t, fixture.Exec("INSERT INTO tz_wide VALUES(5,NULL,'2000-07-01 24:00 AAA-167:59:60BBB')", 1))
	text := fixture.Select("SELECT q::TEXT AS q,(q AT TIME ZONE 'UTC')::TEXT AS utc FROM tz_wide WHERE id=4", []string{"q", "utc"}, []string{"TEXT", "TEXT"}, [][]any{{"12:00:00-168", "12:00:00+00"}})
	timezoneBoth(t, p, text)
	timezoneBoth(t, p, fixture.Select("SELECT q::TEXT AS q FROM tz_wide WHERE id=5", []string{"q"}, []string{"TEXT"}, [][]any{{"24:00:00+169"}}))
	native, target, record := timezoneDestination(t)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	raw := fixture.Select("SELECT q FROM tz_wide WHERE id=4", []string{"q"}, []string{"TIMETZ"}, [][]any{{"12:00:00-168"}})
	raw5 := fixture.Select("SELECT q FROM tz_wide WHERE id=5", []string{"q"}, []string{"TIMETZ"}, [][]any{{"24:00:00+169"}})
	for _, s := range []fixture.Step{raw, raw5} {
		fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, s)
		fixture.RunStep(t, p.Ctx, target, record, s)
	}
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Select("SELECT q,q AT TIME ZONE 'UTC' AS utc FROM tz_wide WHERE id=4", []string{"q", "utc"}, []string{"TIMETZ", "TIMETZ"}, [][]any{{"12:00:00-168", "12:00:00+00"}}))
	unchanged := func() {
		fixture.RunStep(t, p.Ctx, target, record, raw)
		var current bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, native, &current); err != nil || current.String() != dump.String() {
			t.Fatal("restore changed catalog", err)
		}
	}
	canceled, cancel := context.WithCancel(p.Ctx)
	if err := stuntgres.Restore(canceled, native, &timezoneCancelReader{cancel: cancel}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	cancel()
	unchanged()
	if err := stuntgres.Restore(p.Ctx, native, strings.NewReader(dump.String()+"CREATE TABLE bad(v TIMETZ); INSERT INTO bad VALUES('10:00 No/Such_Zone');")); err == nil {
		t.Fatal("unknown zone restore succeeded")
	}
	unchanged()
	for _, input := range []string{"2004-01-01 12:00+00:00:60", "2004-01-01 12:00-16"} {
		s := fixture.ImplicitError("INSERT INTO tz_wide(z) VALUES($1)", fixture.Diagnostic{Code: "22009", Message: `time zone displacement out of range: "` + input + `"`, Context: "unnamed portal parameter $1 = '...'"}, input)
		s.ErrorStage = "execute"
		timezoneBoth(t, p, s)
		timezoneBoth(t, p, fixture.Select("SELECT z::TEXT AS z FROM tz_wide WHERE id=3", []string{"z"}, []string{"TEXT"}, [][]any{{"1970-01-01 00:00:00+00"}}))
	}
}

func TestReferenceTimezoneMulticommandBoundary(t *testing.T) {
	p := fixture.TimezoneReferencePair(t)
	p.Step(t, fixture.Exec("UPDATE tz_witness SET z='epoch' WHERE id=1", 1))
	query := "INSERT INTO tz_witness(z) VALUES('2005-01-01+00'); INSERT INTO tz_witness(z) VALUES('2006-01-01+00')"
	batch := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42601", Message: "cannot insert multiple commands into a prepared statement"})
	batch.ErrorStage = "prepare"
	p.Step(t, fixture.Prepared(batch))
	timezoneBoth(t, p, fixture.Select("SELECT z::TEXT AS z FROM tz_witness", []string{"z"}, []string{"TEXT"}, [][]any{{"1970-01-01 00:00:00+00"}}))
	batch.Args = []any{"2005-01-01+00"}
	batch.ErrorStage = "execute"
	p.Step(t, batch)
	timezoneBoth(t, p, fixture.Select("SELECT z::TEXT AS z FROM tz_witness", []string{"z"}, []string{"TEXT"}, [][]any{{"1970-01-01 00:00:00+00"}}))
	p.Step(t, fixture.Exec(query, 1))
	values := fixture.Select("SELECT z::TEXT AS z FROM tz_witness", []string{"z"}, []string{"TEXT"}, [][]any{{"1970-01-01 00:00:00+00"}, {"2005-01-01 00:00:00+00"}, {"2006-01-01 00:00:00+00"}})
	values.Bag = true
	timezoneBoth(t, p, values)
	query = "UPDATE tz_witness SET z='2005-01-01+00'; nonsense"
	s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "nonsense"`, Position: fixture.Position(strings.Index(query, "nonsense") + 1)})
	s.ErrorStage = "execute"
	p.Step(t, s)
	s.ErrorStage = "prepare"
	p.Step(t, fixture.Prepared(s))
	timezoneBoth(t, p, fixture.Select("SELECT z::TEXT AS z FROM tz_witness WHERE id=1", []string{"z"}, []string{"TEXT"}, [][]any{{"1970-01-01 00:00:00+00"}}))
}
