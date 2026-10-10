package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"testing"
)

const datetimeZoneStoredQuery = "SELECT id,(local AT TIME ZONE 'UTC')::TEXT AS local,(day AT TIME ZONE 'UTC')::TEXT AS day,(fixed AT TIME ZONE 'UTC')::TEXT AS fixed,g::TEXT AS g FROM dz_stored ORDER BY id"

func datetimeZoneStored(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
	run(referenceExec("CREATE TABLE dz_stored(id INTEGER,local TIMESTAMPTZ DEFAULT timezone(TIMESTAMP '2005-04-02 12:00'),day TIMESTAMPTZ DEFAULT date_add(TIMESTAMPTZ '2005-04-02 12:00-07',INTERVAL '1 day'),fixed TIMESTAMPTZ DEFAULT date_add(TIMESTAMPTZ '2005-04-02 12:00-07',INTERVAL '1 day','America/Denver'),g TIMESTAMP GENERATED ALWAYS AS (timezone('UTC',fixed)) STORED,CHECK(timezone('UTC',fixed)>TIMESTAMP '2000-01-01'))", 0))
	run(referenceExec("CREATE INDEX dz_fixed ON dz_stored ((date_subtract(fixed,INTERVAL '1 day','UTC')))", 0))
	run(referenceExec("CREATE VIEW dz_stored_view AS SELECT id,timezone('UTC',fixed) AS civil,date_subtract(fixed,INTERVAL '1 day','UTC') AS previous FROM dz_stored", 0))
	if !b.pg {
		var empty bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &empty); err != nil {
			t.Fatal(err)
		}
		native, _, _ := timezoneDestination(t)
		if err := stuntgres.Restore(p.ctx, native, bytes.NewReader(empty.Bytes())); err != nil {
			t.Fatal(err)
		}
		intervalDumpMatches(t, p.ctx, native, empty.Bytes())
	}
	run(referenceExec("SET TIME ZONE 'America/Denver'", 0))
	run(referenceExec("INSERT INTO dz_stored(id) VALUES(1)", 1))
	run(referenceExec("SET TIME ZONE 'UTC'", 0))
	run(referencePrepared(referenceExec("INSERT INTO dz_stored(id) VALUES($1)", 1, 2)))
	run(referenceExec("INSERT INTO dz_stored(id,local,day,fixed) VALUES(3,NULL,NULL,NULL)", 1))
	want := [][]any{
		{int64(1), "2005-04-02 19:00:00", "2005-04-03 18:00:00", "2005-04-03 18:00:00", "2005-04-03 18:00:00"},
		{int64(2), "2005-04-02 12:00:00", "2005-04-03 19:00:00", "2005-04-03 18:00:00", "2005-04-03 18:00:00"},
		{int64(3), nil, nil, nil, nil},
	}
	s := referenceSelect(datetimeZoneStoredQuery, []string{"id", "local", "day", "fixed", "g"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT"}, want)
	datetimeArithmeticBoth(t, p, b, c, s)
	datetimeArithmeticBoth(t, p, b, c, referenceSelect("SELECT id,civil::TEXT AS civil,timezone('UTC',previous)::TEXT AS previous FROM dz_stored_view ORDER BY id", []string{"id", "civil", "previous"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "2005-04-03 18:00:00", "2005-04-02 18:00:00"}, {int64(2), "2005-04-03 18:00:00", "2005-04-02 18:00:00"}, {int64(3), nil, nil}}))
	datetimeArithmeticBoth(t, p, b, c, referenceSelect("SELECT timezone('UTC',fixed)::TEXT AS civil,COUNT(*) AS n FROM dz_stored WHERE fixed IS NOT NULL GROUP BY timezone('UTC',fixed) ORDER BY timezone('UTC',fixed)", []string{"civil", "n"}, []string{"TEXT", "INT8"}, [][]any{{"2005-04-03 18:00:00", int64(2)}}))
	for _, expr := range []string{"timezone(local)", "date_add(local,INTERVAL '1 day')", "timezone('UTC',TIMETZ '12:00+00')", "date_add(DATE '2004-01-01',INTERVAL '0','UTC')"} {
		d := referenceImplicitError("CREATE INDEX dz_bad ON dz_stored (("+expr+"))", referenceDiagnostic{Code: "42P17", Message: "functions in index expression must be marked IMMUTABLE"})
		run(d)
		d.prepared = true
		run(d)
	}
	d := referenceImplicitError("CREATE TABLE dz_bad_generated(z TIMESTAMPTZ,g TIMESTAMPTZ GENERATED ALWAYS AS (date_add(z,INTERVAL '1 day')) STORED)", referenceDiagnostic{Code: "42P17", Message: "generation expression is not immutable"})
	datetimeArithmeticBoth(t, p, b, c, d)
	if b.pg {
		return
	} // Native logical controls have no PostgreSQL API analogue.
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"timezone", "date_add", "date_subtract", "DEFAULT", "GENERATED", "CHECK", "dz_stored_view", "dz_fixed"} {
		if !strings.Contains(dump.String(), needle) {
			t.Fatal("lost symbolic expression", needle, dump.String())
		}
	}
	native, wrapped, record := timezoneDestination(t)
	target, err := wrapped.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	trun := func(s referenceStep) { referenceRunStep(t, p.ctx, target, record, s) }
	trun(referenceExec("SET TIME ZONE 'America/Denver'", 0))
	trun(referenceExec("SET IntervalStyle='iso_8601'", 0))
	if err := stuntgres.Restore(p.ctx, native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	trun(referenceSelect("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"America/Denver"}}))
	trun(referenceSelect("SHOW IntervalStyle", []string{"IntervalStyle"}, []string{"TEXT"}, [][]any{{"iso_8601"}}))
	trun(s)
	intervalDumpMatches(t, p.ctx, native, dump.Bytes())
	held := timezoneHeld(t, p, target, datetimeZoneStoredQuery)
	oldRecord := &referenceRecorder{rawBackend: referenceRawNative}
	old, err := target.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, oldRecord), datetimeZoneStoredQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	bad := dump.String() + "CREATE TABLE dz_restore_bad(v TIMESTAMPTZ DEFAULT timezone('No/Such_Zone',TIMESTAMP '2004-01-01')); INSERT INTO dz_restore_bad DEFAULT VALUES;"
	if err := stuntgres.Restore(p.ctx, native, strings.NewReader(bad)); err == nil {
		t.Fatal("bad zone restore succeeded")
	} else {
		referenceAssertDiagnostic(t, err, diagnosticDatetimePointer("22023", `time zone "No/Such_Zone" not recognized`))
	}
	intervalDumpMatches(t, p.ctx, native, dump.Bytes())
	failure := errors.New("datetime zone reader failure")
	if err := stuntgres.Restore(p.ctx, native, timezoneFailingReader{failure}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(p.ctx)
	if err := stuntgres.Restore(ctx, native, &timezoneCancelReader{cancel: cancel}); !errors.Is(err, context.Canceled) {
		cancel()
		t.Fatal(err)
	}
	cancel()
	intervalDumpMatches(t, p.ctx, native, dump.Bytes())
	if err := stuntgres.Reset(p.ctx, native); err != nil {
		t.Fatal(err)
	}
	referenceRunStep(t, p.ctx, timezoneAdmittedRows{referenceExecutor: target, rows: old}, oldRecord, s)
	if err := stuntgres.Restore(p.ctx, native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	referenceRunStep(t, p.ctx, held, record, s)
	trun(referenceExec("INSERT INTO dz_stored(id) VALUES(4)", 1))
	trun(referenceSelect("SELECT timezone('UTC',local)::TEXT AS local,timezone('UTC',day)::TEXT AS day,timezone('UTC',fixed)::TEXT AS fixed FROM dz_stored WHERE id=4", []string{"local", "day", "fixed"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"2005-04-02 19:00:00", "2005-04-03 18:00:00", "2005-04-03 18:00:00"}}))
	if p.postgres != nil {
		pg, err := p.postgres.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer pg.Close()
		if _, err := pg.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatal("emitted SQL replay", err, dump.String())
		}
		referenceRunStep(t, p.ctx, pg, p.postgresRecord, s)
		for _, q := range []string{"DROP VIEW dz_stored_view", "DROP TABLE dz_stored", "DROP TABLE tz_witness"} {
			referenceRunStep(t, p.ctx, pg, p.postgresRecord, referenceExec(q, 0))
		}
	}
}

func TestReferenceDatetimeZoneStoredDefinitionsAndNativeControls(t *testing.T) {
	datetimeZoneScenario(t, datetimeZoneStored)
}

func datetimeZoneAtomicity(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
	run(referenceExec("CREATE TABLE dz_write(id INTEGER,zone TEXT,z TIMESTAMPTZ)", 0))
	// This helper owns its write fixture. Remove it after all assertions, before
	// a composed smoke's subsequent Dump/PG replay can copy it into that backend.
	defer run(referenceExec("DROP TABLE dz_write", 0))
	run(referenceExec("INSERT INTO dz_write VALUES(0,'UTC','2004-01-01+00'),(1,'No/Such_Zone','2004-01-01+00')", 2))
	sentinel := referenceSelect("SELECT id,z::TEXT AS z FROM dz_write ORDER BY id", []string{"id", "z"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), "2004-01-01 00:00:00+00"}, {int64(1), "2004-01-01 00:00:00+00"}})
	for _, q := range []string{
		"INSERT INTO dz_write(id,z) VALUES(2,date_add(TIMESTAMPTZ '2004-01-01+00',INTERVAL '1 day','UTC')),(3,date_add(TIMESTAMPTZ '2004-01-01+00',INTERVAL '1 day',$1::TEXT))",
		"UPDATE dz_write SET z=date_subtract(z,INTERVAL '1 day',CASE WHEN id=0 THEN 'UTC' ELSE $1::TEXT END)",
	} {
		for _, prepared := range []bool{false, true} {
			d := referenceImplicitError(q, referenceDiagnostic{Code: "22023", Message: `time zone "No/Such_Zone" not recognized`}, "No/Such_Zone")
			d.prepared, d.errorStage = prepared, "execute"
			run(d)
			run(sentinel)
		}
	}
	tx, err := c.BeginTx(p.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	d := referenceImplicitError("UPDATE dz_write SET z=timezone(zone,TIMESTAMP '2004-01-02')", referenceDiagnostic{Code: "22023", Message: `time zone "No/Such_Zone" not recognized`})
	referenceRunStep(t, p.ctx, tx, b.recorder, d)
	referenceRunStep(t, p.ctx, tx, b.recorder, referenceImplicitError("SELECT id FROM dz_write", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	run(sentinel)
	run(referenceExec("UPDATE dz_write SET z=date_add(z,INTERVAL '1 day','UTC') WHERE id=0", 1))
	if !b.pg {
		q := "INSERT INTO dz_write(id,z) VALUES(5,date_add(TIMESTAMPTZ '2004-01-01+00',INTERVAL '0','UTC'))"
		h := timezoneHeld(t, p, c, q)
		ctx, cancel := context.WithCancel(p.ctx)
		cancel()
		for _, x := range []referenceExecutor{c, h} {
			result, err := x.ExecContext(ctx, q)
			if result != nil || !errors.Is(err, context.Canceled) {
				t.Fatal(result, err)
			}
		}
		run(referenceSelect("SELECT COUNT(*) AS n FROM dz_write", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
	}
}

func TestReferenceDatetimeZoneCancellationAtomicity(t *testing.T) {
	datetimeZoneScenario(t, datetimeZoneAtomicity)
}

func TestReferenceDatetimeZoneFeatureSmoke(t *testing.T) {
	datetimeZoneScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		datetimeZoneOverloads(t, p, b, c)
		datetimeZoneOffsets(t, p, b, c)
		datetimeZoneCalendar(t, p, b, c)
		datetimeZoneExecution(t, p, b, c)
		datetimeZoneDiagnostics(t, p, b, c)
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET IntervalStyle='postgres'", 0))
		datetimeZoneAtomicity(t, p, b, c)
		datetimeZoneStored(t, p, b, c)
	})
}
