package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceTimezoneWideOffsetNativeRoundTrip(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		backend.db.SetMaxOpenConns(1)
		backend.db.SetMaxIdleConns(1)
	}
	p.step(t, referenceExec("SET TIME ZONE 'UTC'", 0))
	p.step(t, referenceExec("CREATE TABLE tz_wide(id INTEGER,z TIMESTAMPTZ,q TIMETZ)", 0))
	p.step(t, referenceExec("INSERT INTO tz_wide VALUES(1,'-infinity','00:00+15:59:59'),(2,'infinity','24:00-15:59:59'),(3,'epoch',NULL)", 3))
	timezoneBoth(t, p, referenceSelect("SELECT z::TEXT AS z FROM tz_wide ORDER BY z", []string{"z"}, []string{"TEXT"}, [][]any{{"-infinity"}, {"1970-01-01 00:00:00+00"}, {"infinity"}}))
	timezoneBoth(t, p, referenceSelect("SELECT TIMESTAMPTZ '2004-01-01 24:00+00'::TEXT AS a,TIMESTAMPTZ '2004-01-01 00:00+15:59:59'::TEXT AS b FROM tz_wide WHERE id=1", []string{"a", "b"}, []string{"TEXT", "TEXT"}, [][]any{{"2004-01-02 00:00:00+00", "2003-12-31 08:00:01+00"}}))
	for _, backend := range p.backends() {
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("SET TIME ZONE 'UTC167:59:60'", 0))
		query := "SELECT (TIMETZ '12:00')::TEXT AS q FROM tz_wide WHERE id=1"
		held := timezoneHeld(t, p, backend.db, query)
		var rawHeld referenceHeldStatement
		rawQuery := "SELECT TIMETZ '12:00' AS q FROM tz_wide WHERE id=1"
		if !backend.pg {
			rawHeld = timezoneHeld(t, p, backend.db, rawQuery)
		}
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("INSERT INTO tz_wide VALUES(4,NULL,'12:00')", 1))
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("SET TIME ZONE 'UTC'", 0))
		referenceRunStep(t, p.ctx, held, backend.recorder, referenceSelect(query, []string{"q"}, []string{"TEXT"}, [][]any{{"12:00:00-168"}}))
		if !backend.pg {
			referenceRunStep(t, p.ctx, rawHeld, backend.recorder, referenceSelect(rawQuery, []string{"q"}, []string{"TIMETZ"}, [][]any{{"12:00:00-168"}}))
		}
	}
	p.step(t, referenceExec("INSERT INTO tz_wide VALUES(5,NULL,'2000-07-01 24:00 AAA-167:59:60BBB')", 1))
	text := referenceSelect("SELECT q::TEXT AS q,(q AT TIME ZONE 'UTC')::TEXT AS utc FROM tz_wide WHERE id=4", []string{"q", "utc"}, []string{"TEXT", "TEXT"}, [][]any{{"12:00:00-168", "12:00:00+00"}})
	timezoneBoth(t, p, text)
	timezoneBoth(t, p, referenceSelect("SELECT q::TEXT AS q FROM tz_wide WHERE id=5", []string{"q"}, []string{"TEXT"}, [][]any{{"24:00:00+169"}}))
	native, target, record := timezoneDestination(t)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	raw := referenceSelect("SELECT q FROM tz_wide WHERE id=4", []string{"q"}, []string{"TIMETZ"}, [][]any{{"12:00:00-168"}})
	raw5 := referenceSelect("SELECT q FROM tz_wide WHERE id=5", []string{"q"}, []string{"TIMETZ"}, [][]any{{"24:00:00+169"}})
	for _, s := range []referenceStep{raw, raw5} {
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, s)
		referenceRunStep(t, p.ctx, target, record, s)
	}
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SELECT q,q AT TIME ZONE 'UTC' AS utc FROM tz_wide WHERE id=4", []string{"q", "utc"}, []string{"TIMETZ", "TIMETZ"}, [][]any{{"12:00:00-168", "12:00:00+00"}}))
	unchanged := func() {
		referenceRunStep(t, p.ctx, target, record, raw)
		var current bytes.Buffer
		if err := stuntgres.Dump(p.ctx, native, &current); err != nil || current.String() != dump.String() {
			t.Fatal("restore changed catalog", err)
		}
	}
	canceled, cancel := context.WithCancel(p.ctx)
	if err := stuntgres.Restore(canceled, native, &timezoneCancelReader{cancel: cancel}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	cancel()
	unchanged()
	if err := stuntgres.Restore(p.ctx, native, strings.NewReader(dump.String()+"CREATE TABLE bad(v TIMETZ); INSERT INTO bad VALUES('10:00 No/Such_Zone');")); err == nil {
		t.Fatal("unknown zone restore succeeded")
	}
	unchanged()
	for _, input := range []string{"2004-01-01 12:00+00:00:60", "2004-01-01 12:00-16"} {
		s := referenceImplicitError("INSERT INTO tz_wide(z) VALUES($1)", referenceDiagnostic{Code: "22009", Message: `time zone displacement out of range: "` + input + `"`, Context: "unnamed portal parameter $1 = '...'"}, input)
		s.errorStage = "execute"
		timezoneBoth(t, p, s)
		timezoneBoth(t, p, referenceSelect("SELECT z::TEXT AS z FROM tz_wide WHERE id=3", []string{"z"}, []string{"TEXT"}, [][]any{{"1970-01-01 00:00:00+00"}}))
	}
}

func TestReferenceTimezoneMulticommandBoundary(t *testing.T) {
	p := timezoneReferencePair(t)
	p.step(t, referenceExec("UPDATE tz_witness SET z='epoch' WHERE id=1", 1))
	query := "INSERT INTO tz_witness(z) VALUES('2005-01-01+00'); INSERT INTO tz_witness(z) VALUES('2006-01-01+00')"
	if _, err := p.memory.ExecContext(p.ctx, query); err == nil {
		t.Fatal("native accepted unsupported multiple commands")
	}
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SELECT z::TEXT AS z FROM tz_witness", []string{"z"}, []string{"TEXT"}, [][]any{{"1970-01-01 00:00:00+00"}}))
	if p.postgres != nil {
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec(query, 1))
		s := referenceSelect("SELECT z::TEXT AS z FROM tz_witness", []string{"z"}, []string{"TEXT"}, [][]any{{"1970-01-01 00:00:00+00"}, {"2005-01-01 00:00:00+00"}, {"2006-01-01 00:00:00+00"}})
		s.bag = true
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
	}
	query = "UPDATE tz_witness SET z='2005-01-01+00'; nonsense"
	s := referenceImplicitError(query, referenceDiagnostic{Code: "42601", Message: `syntax error at or near "nonsense"`, Position: referencePosition(strings.Index(query, "nonsense") + 1)})
	s.errorStage = "execute"
	p.step(t, s)
	s.errorStage = "prepare"
	p.step(t, referencePrepared(s))
	timezoneBoth(t, p, referenceSelect("SELECT z::TEXT AS z FROM tz_witness WHERE id=1", []string{"z"}, []string{"TEXT"}, [][]any{{"1970-01-01 00:00:00+00"}}))
}
