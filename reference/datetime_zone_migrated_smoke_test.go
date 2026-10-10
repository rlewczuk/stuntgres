package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres"
	"testing"
	"time"
)

// Complete migration of smoke/TestTimezoneSmoke, retaining its two physical
// sessions, imports, raw metadata, settings failures and native control routes.
func datetimeZoneMigratedSmoke(t *testing.T, p *referencePair, backend referenceBackend, a *sql.Conn) {
	backend.db.SetMaxOpenConns(2)
	defer backend.db.SetMaxOpenConns(1)
	b, err := backend.db.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	run := func(c *sql.Conn, s referenceStep) { referenceRunStep(t, p.ctx, c, backend.recorder, s) }
	check := func(c *sql.Conn, query string, names, types []string, values ...any) {
		run(c, referenceSelect(query, names, types, [][]any{values}))
	}
	run(a, referenceExec("CREATE TABLE dz_legacy(id INTEGER,z TIMESTAMPTZ(3),q TIME(3) WITH TIME ZONE)", 0))
	run(a, referenceExec("INSERT INTO dz_legacy VALUES(1,'2004-10-19 10:23:54+02','05:34:17-05')", 1))
	run(b, referenceExec("SET DateStyle='ISO,MDY'", 0))
	run(b, referenceExec("SET TIME ZONE 'America/New_York'", 0))
	check(a, "SELECT z::TEXT AS z,z=TIMESTAMPTZ '2004-10-19 08:23:54+00' AS same FROM dz_legacy", []string{"z", "same"}, []string{"TEXT", "BOOL"}, "2004-10-19 08:23:54+00", true)
	check(b, "SELECT z::TEXT AS z,z=TIMESTAMPTZ '2004-10-19 08:23:54+00' AS same FROM dz_legacy", []string{"z", "same"}, []string{"TEXT", "BOOL"}, "2004-10-19 04:23:54-04", true)
	check(a, "SELECT TIMESTAMPTZ '2018-03-11 02:30 America/New_York'::TEXT AS gap,TIMESTAMPTZ '2018-11-04 01:30 America/New_York'::TEXT AS fold,TIMESTAMPTZ(3) '2004-01-01 00:00:00.9995+00'::TEXT AS rounded FROM dz_legacy", []string{"gap", "fold", "rounded"}, []string{"TEXT", "TEXT", "TEXT"}, "2018-03-11 07:30:00+00", "2018-11-04 06:30:00+00", "2004-01-01 00:00:01+00")
	var clock any = "10:34:17+00"
	if backend.pg {
		clock = time.Date(0, 1, 1, 10, 34, 17, 0, time.UTC)
	}
	check(b, "SELECT (TIMESTAMPTZ '2004-07-01 12:00' AT TIME ZONE 'UTC')::TEXT AS t,DATE '2004-10-19'=TIMESTAMPTZ '2004-10-19 04:00+00' AS same,TIMETZ '05:34:17-05' AT TIME ZONE 'UTC' AS q FROM dz_legacy", []string{"t", "same", "q"}, []string{"TEXT", "BOOL", "TIMETZ"}, "2004-07-01 16:00:00", true, clock)
	run(b, referenceExec("SET DateStyle='ISO,DMY'", 0))
	var date any = "2003-02-01"
	if backend.pg {
		date = time.Date(2003, 2, 1, 0, 0, 0, 0, time.UTC)
	}
	check(b, "SELECT DATE '01/02/03' AS d FROM dz_legacy", []string{"d"}, []string{"DATE"}, date)
	run(b, referenceExec("SET DateStyle='SQL'", 0))
	check(b, "SELECT DATE '1997-12-17'::TEXT AS d FROM dz_legacy", []string{"d"}, []string{"TEXT"}, "17/12/1997")
	// pq does not decode SQL-style DATE wire text. The old native raw contract
	// is retained independently, without pretending that adapter decode parity.
	if !backend.pg {
		check(b, "SELECT DATE '1997-12-17' AS d FROM dz_legacy", []string{"d"}, []string{"DATE"}, "17/12/1997")
	}
	check(a, "SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, "ISO, MDY")
	run(a, referenceExec("SET TIME ZONE 5.5", 0))
	check(a, "SELECT z::TEXT AS z FROM dz_legacy", []string{"z"}, []string{"TEXT"}, "2004-10-19 13:53:54+05:30")
	run(a, referenceExec("SET TIME ZONE INTERVAL '-08:00' HOUR TO MINUTE", 0))
	clock = "05:34:17-05"
	if backend.pg {
		clock = time.Date(0, 1, 1, 5, 34, 17, 0, time.FixedZone("", -18000)).UTC()
	}
	check(a, "SELECT q FROM dz_legacy", []string{"q"}, []string{"TIMETZ"}, clock)
	run(a, referenceExec("RESET TIME ZONE", 0))
	defaultZone := "UTC"
	if backend.pg {
		defaultZone = "Etc/UTC"
	}
	for _, query := range []string{"BEGIN", "SET LOCAL TIME ZONE 'Asia/Tokyo'", "COMMIT"} {
		run(b, referenceExec(query, 0))
	}
	check(b, "SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, "America/New_York")
	imported := time.Date(2004, 7, 1, 18, 0, 0, 0, time.FixedZone("east", 7200))
	run(a, referencePrepared(referenceExec("INSERT INTO dz_legacy VALUES($1,$2,$3)", 1, 2, imported, nil)))
	check(a, "SELECT z::TEXT AS z,q FROM dz_legacy WHERE id=2", []string{"z", "q"}, []string{"TEXT", "TIMETZ"}, "2004-07-01 16:00:00+00", nil)
	nulls := referenceSelect("SELECT NULL::TIMESTAMPTZ AS z,NULL::TIMETZ AS q FROM dz_legacy WHERE id=1", []string{"z", "q"}, []string{"TIMESTAMPTZ", "TIMETZ"}, [][]any{{nil, nil}})
	nulls.checkMetadata = datetimeZoneLegacyMetadata(backend.pg, false)
	run(a, nulls)
	columns := referenceSelect("SELECT z,q FROM dz_legacy WHERE id=2", []string{"z", "q"}, []string{"TIMESTAMPTZ", "TIMETZ"}, [][]any{{time.Date(2004, 7, 1, 16, 0, 0, 0, time.UTC), nil}})
	columns.checkMetadata = datetimeZoneLegacyMetadata(backend.pg, true)
	run(a, columns)
	for _, tc := range []struct{ query, code, message, detail, position string }{
		{"SET TIME ZONE 'No/Such_Zone'", "22023", `invalid value for parameter "TimeZone": "No/Such_Zone"`, "", ""},
		{"SELECT TIMESTAMPTZ '2004-10-19 10:00+16' FROM dz_legacy", "22009", `time zone displacement out of range: "2004-10-19 10:00+16"`, "", "20"},
		{"SET DateStyle='ISO,SQL'", "22023", `invalid value for parameter "DateStyle": "ISO,SQL"`, `Conflicting "DateStyle" specifications.`, ""},
	} {
		diagnostic := controlsReferenceDiagnostic(tc.code, tc.message)
		diagnostic.Detail, diagnostic.Position = tc.detail, tc.position
		run(a, referenceErrorDiagnostic(tc.query, diagnostic))
	}
	check(a, "SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, defaultZone)
	check(a, "SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, "ISO, MDY")
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if backend.pg {
		return
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	target, wrapped, record := timezoneDestination(t)
	c, err := wrapped.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	referenceRunStep(t, p.ctx, c, record, referenceExec("SET TIME ZONE 'UTC'", 0))
	if err := stuntgres.Restore(p.ctx, target, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	referenceRunStep(t, p.ctx, c, record, referenceSelect("SELECT z::TEXT AS z,q FROM dz_legacy WHERE id=1", []string{"z", "q"}, []string{"TEXT", "TIMETZ"}, [][]any{{"2004-10-19 08:23:54+00", "05:34:17-05"}}))
	canceled, cancel := context.WithCancel(p.ctx)
	cancel()
	if err := stuntgres.Restore(canceled, target, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	referenceRunStep(t, p.ctx, c, record, referenceSelect("SELECT z::TEXT AS z FROM dz_legacy WHERE id=2", []string{"z"}, []string{"TEXT"}, [][]any{{"2004-07-01 16:00:00+00"}}))
}

func datetimeZoneLegacyMetadata(pg, columns bool) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		metadata, err := rows.ColumnTypes()
		if err != nil || len(metadata) != 2 {
			t.Fatal(metadata, err)
		}
		for i, typ := range []string{"TIMESTAMPTZ", "TIMETZ"} {
			column := metadata[i]
			if column.DatabaseTypeName() != typ {
				t.Fatal(column)
			}
			if !pg {
				scan := reflect.TypeFor[time.Time]()
				if i == 1 {
					scan = reflect.TypeFor[string]()
				}
				if column.ScanType() != scan {
					t.Fatal(column.ScanType(), scan)
				}
			}
			nullable, known := column.Nullable()
			wantKnown := !pg && columns
			if known != wantKnown || nullable != wantKnown {
				t.Fatal("temporal nullability", nullable, known, wantKnown)
			}
			precision, scale, decimal := column.DecimalSize()
			if precision != 0 || scale != 0 || decimal {
				t.Fatal("temporal decimal metadata", precision, scale, decimal)
			}
		}
	}
}

func TestReferenceDatetimeZoneMigratedTimezoneSmoke(t *testing.T) {
	datetimeZoneScenario(t, datetimeZoneMigratedSmoke)
}
