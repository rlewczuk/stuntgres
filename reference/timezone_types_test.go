package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"
	"time"
)

// Independent constants are asserted by the existing recorded public-driver
// fixture before backend parity. Each route owns a single physical session.
func timezoneReferencePair(t *testing.T) *referencePair {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		backend.db.SetMaxOpenConns(1)
		backend.db.SetMaxIdleConns(1)
	}
	p.step(t, referenceExec("SET TIME ZONE 'UTC'", 0))
	p.step(t, referenceExec("SET DateStyle='ISO, MDY'", 0))
	p.step(t, referenceExec("CREATE TABLE tz_witness(id INTEGER,z TIMESTAMPTZ(3),q TIMETZ(3),d DATE,c TIMESTAMP)", 0))
	p.step(t, referenceExec("INSERT INTO tz_witness VALUES(1,NULL,NULL,NULL,NULL)", 1))
	return p
}

// Capture each backend's configured default separately from the explicit UTC
// baseline: PostgreSQL's pinned container boots in Etc/UTC, not the native UTC.
func timezoneDefaultZone(t *testing.T, p *referencePair, conn referenceExecutor, postgres bool) string {
	t.Helper()
	if _, err := conn.ExecContext(p.ctx, "RESET TIME ZONE"); err != nil {
		t.Fatal(err)
	}
	rows, err := conn.QueryContext(p.ctx, "SHOW TIME ZONE")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("missing default TimeZone", rows.Err())
	}
	var zone string
	if err := rows.Scan(&zone); err != nil {
		t.Fatal(err)
	}
	want := "UTC"
	if postgres {
		want = "Etc/UTC"
	}
	if zone != want || rows.Next() || rows.Err() != nil {
		t.Fatal("configured default TimeZone", zone, want, rows.Err())
	}
	return zone
}

func timezoneTextStep(expression, want string) referenceStep {
	return referenceSelect("SELECT ("+expression+")::TEXT AS v FROM tz_witness WHERE id=1", []string{"v"}, []string{"TEXT"}, [][]any{{want}})
}

func timezoneBoth(t *testing.T, p *referencePair, s referenceStep) {
	t.Helper()
	p.step(t, s)
	p.step(t, referencePrepared(s))
}

func TestReferenceTimezoneInputPrecisionAndBounds(t *testing.T) {
	p := timezoneReferencePair(t)
	for _, tc := range []struct{ expression, want string }{
		{"TIMESTAMPTZ '2004-10-19 10:23:54+02'", "2004-10-19 08:23:54+00"},
		{"TIMESTAMPTZ '2004-07-01 12:00 EST'", "2004-07-01 17:00:00+00"},
		{"TIMESTAMPTZ '2004-07-01 12:00 EDT'", "2004-07-01 16:00:00+00"},
		{"TIMESTAMPTZ '2004-07-01 12:00 aMeRiCa/NeW_yOrK'", "2004-07-01 16:00:00+00"},
		{"TIMESTAMPTZ '2004-07-01 12:00 aMeRiCa/NeW_YoRk'", "2004-07-01 16:00:00+00"},
		{"TIMESTAMPTZ '2004-07-01 12:00 US/Eastern'", "2004-07-01 16:00:00+00"},
		{"TIMESTAMPTZ '2004-01-01 00:00-05:30:15'", "2004-01-01 05:30:15+00"},
		{"TIMESTAMPTZ '2004-01-02 00:00+15:59:59'", "2004-01-01 08:00:01+00"},
		{"TIMESTAMPTZ '2004-01-01 00:00-15:59:59'", "2004-01-01 15:59:59+00"},
		{"'2004-01-01 00:00:00.9995+00'::TEXT::TIMESTAMP(3) WITH TIME ZONE", "2004-01-01 00:00:01+00"},
		{"'2004-01-01 23:59:59.5+00'::TEXT::TIMESTAMP(0) WITH TIME ZONE", "2004-01-02 00:00:00+00"},
		{"'2004-01-01 00:00:00.123456+00'::TEXT::TIMESTAMP(6) WITH TIME ZONE", "2004-01-01 00:00:00.123456+00"},
		{"TIMESTAMPTZ 'epoch'", "1970-01-01 00:00:00+00"},
		{"TIMESTAMPTZ '0001-01-01 00:00+00 BC'", "0001-01-01 00:00:00+00 BC"},
		{"TIMESTAMPTZ '4714-11-24 00:00+00 BC'", "4714-11-24 00:00:00+00 BC"},
		{"TIMESTAMPTZ '294276-12-31 23:59:59.999999+00'", "294276-12-31 23:59:59.999999+00"},
		{"TIMESTAMPTZ '2004-01-01 24:00+00'", "2004-01-02 00:00:00+00"},
		{"TIMESTAMPTZ 'infinity'", "infinity"},
		{"TIMESTAMPTZ '-infinity'", "-infinity"},
		{"TIMESTAMP '2004-10-19 10:23:54+02'", "2004-10-19 10:23:54"},
		{"TIMETZ '24:00+02'", "24:00:00+02"},
		{"'23:59:59.9995-05'::TEXT::TIME(3) WITH TIME ZONE", "24:00:00-05"},
	} {
		t.Run(tc.expression, func(t *testing.T) { timezoneBoth(t, p, timezoneTextStep(tc.expression, tc.want)) })
	}
	timezoneBoth(t, p, referenceSelect("SELECT TIMESTAMPTZ(3) '2004-01-01 00:00:00.9995+00'::TEXT AS z,TIMESTAMP '2004-01-01 12:00+02'::TEXT AS c FROM tz_witness", []string{"z", "c"}, []string{"TEXT", "TEXT"}, [][]any{{"2004-01-01 00:00:01+00", "2004-01-01 12:00:00"}}))
	p.step(t, referenceExec("SET TIME ZONE 'America/New_York'", 0))
	timezoneBoth(t, p, timezoneTextStep("TIMESTAMPTZ '2004-07-01 12:00' AT TIME ZONE 'UTC'", "2004-07-01 16:00:00"))
}

func TestReferenceTimezoneMetadataParametersAndFreeze(t *testing.T) {
	p := timezoneReferencePair(t)
	instant := time.Date(2004, 10, 19, 10, 23, 54, 123456000, time.FixedZone("written", 7200))
	for i, prepared := range []bool{false, true} {
		s := referenceExec("INSERT INTO tz_witness(id,z,q) VALUES($1,$2,$3)", 1, int64(i+2), instant, "05:34:17.1236-05")
		s.prepared = prepared
		p.step(t, s)
	}
	timezoneBoth(t, p, referenceSelect("SELECT z,q::TEXT AS q FROM tz_witness ORDER BY id", []string{"z", "q"}, []string{"TIMESTAMPTZ", "TEXT"}, [][]any{{nil, nil}, {instant.UTC().Truncate(time.Millisecond), "05:34:17.124-05"}, {instant.UTC().Truncate(time.Millisecond), "05:34:17.124-05"}}))
	timezoneBoth(t, p, referenceSelect("SELECT $1::TIMESTAMPTZ AS z,$2::TIMETZ AS q FROM tz_witness WHERE id=1", []string{"z", "q"}, []string{"TIMESTAMPTZ", "TIMETZ"}, [][]any{{nil, nil}}, nil, nil))
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("SET TIME ZONE 'America/New_York'", 0))
			frozenQuery := "SELECT TIMESTAMPTZ '2004-07-01 12:00' AS z,(TIMETZ '05:34:17-05')::TEXT AS q FROM tz_witness WHERE id=1"
			frozen := timezoneHeld(t, p, backend.db, frozenQuery)
			dynamicQuery := "SELECT ($1::TIMESTAMPTZ)::TEXT AS v FROM tz_witness WHERE id=1"
			dynamic := timezoneHeld(t, p, backend.db, dynamicQuery)
			run(referenceExec("SET TIME ZONE 'UTC'", 0))
			referenceRunStep(t, p.ctx, frozen, backend.recorder, referenceSelect(frozenQuery, []string{"z", "q"}, []string{"TIMESTAMPTZ", "TEXT"}, [][]any{{time.Date(2004, 7, 1, 16, 0, 0, 0, time.UTC), "05:34:17-05"}}))
			for _, tc := range []struct{ zone, want string }{{"UTC", "2004-07-01 12:00:00+00"}, {"America/New_York", "2004-07-01 12:00:00-04"}} {
				run(referenceExec("SET TIME ZONE '"+tc.zone+"'", 0))
				referenceRunStep(t, p.ctx, dynamic, backend.recorder, referenceSelect(dynamicQuery, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, "2004-07-01 12:00"))
			}
			run(referenceExec("SET TIME ZONE 'UTC'", 0))
			rows, err := backend.db.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder), "SELECT z,q FROM tz_witness WHERE id=1")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			types, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			if len(types) != 2 || types[0].DatabaseTypeName() != "TIMESTAMPTZ" || types[1].DatabaseTypeName() != "TIMETZ" {
				t.Fatal(types)
			}
			if !backend.pg {
				if types[0].ScanType() != reflect.TypeOf(time.Time{}) || types[1].ScanType() != reflect.TypeOf("") {
					t.Fatal(types)
				}
				for _, typ := range types {
					precision, scale, ok := typ.DecimalSize()
					if ok || precision != 0 || scale != 0 {
						t.Fatalf("temporal type must not claim numeric precision: %s %d %d %t", typ.Name(), precision, scale, ok)
					}
					nullable, ok := typ.Nullable()
					if !ok || !nullable {
						t.Fatal("NULL metadata", typ)
					}
				}
			}
			referenceRunStep(t, p.ctx, timezoneAdmittedRows{referenceExecutor: backend.db, rows: rows}, backend.recorder, referenceSelect("SELECT z,q FROM tz_witness WHERE id=1", []string{"z", "q"}, []string{"TIMESTAMPTZ", "TIMETZ"}, [][]any{{nil, nil}}))
			if backend.pg {
				// Native exposes SQL type/scan metadata above, not pg_type or
				// a public OID API. Assert the fixed release OIDs on PostgreSQL.
				for _, tc := range []struct {
					name string
					oid  int64
				}{{"timestamptz", 1184}, {"timetz", 1266}} {
					for _, prepared := range []bool{false, true} {
						s := referenceSelect(fmt.Sprintf("SELECT oid::BIGINT AS oid FROM pg_catalog.pg_type WHERE typname='%s'", tc.name), []string{"oid"}, []string{"INT8"}, [][]any{{tc.oid}})
						s.prepared = prepared
						run(s)
					}
				}
			}
		})
	}
}

func TestReferenceTimezoneDefaultFreezeAndNativeOwnership(t *testing.T) {
	p := timezoneReferencePair(t)
	p.step(t, referenceExec("SET TIME ZONE 'America/New_York'", 0))
	p.step(t, referenceExec("CREATE TABLE tz_defaults(id INTEGER,z TIMESTAMPTZ DEFAULT TIMESTAMPTZ '2004-07-01 12:00',q TIMETZ DEFAULT TIMETZ '05:34:17-05')", 0))
	p.step(t, referenceExec("SET TIME ZONE 'UTC'", 0))
	p.step(t, referenceExec("INSERT INTO tz_defaults(id) VALUES(1)", 1))
	p.step(t, referencePrepared(referenceExec("INSERT INTO tz_defaults(id) VALUES(2)", 1)))
	timezoneBoth(t, p, referenceSelect("SELECT id,z::TEXT AS z,q::TEXT AS q FROM tz_defaults ORDER BY id", []string{"id", "z", "q"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "2004-07-01 16:00:00+00", "05:34:17-05"}, {int64(2), "2004-07-01 16:00:00+00", "05:34:17-05"}}))
	// Native infinity follows the existing TIMESTAMP fresh-byte adapter policy.
	// PostgreSQL wire representation is checked independently by TEXT elsewhere.
	p.step(t, referenceExec("INSERT INTO tz_witness(id,z) VALUES(2,'infinity'),(3,'infinity')", 2))
	for _, prepared := range []bool{false, true} {
		var rows *sql.Rows
		var err error
		record := &referenceRecorder{rawBackend: referenceRawNative}
		ctx := context.WithValue(p.ctx, referenceRecorderKey{}, record)
		query := "SELECT z FROM tz_witness WHERE z IS NOT NULL ORDER BY id"
		if prepared {
			stmt, e := p.memory.PrepareContext(p.ctx, query)
			if e != nil {
				t.Fatal(e)
			}
			defer stmt.Close()
			rows, err = stmt.QueryContext(ctx)
		} else {
			rows, err = p.memory.QueryContext(ctx, query)
		}
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		meta, err := rows.ColumnTypes()
		if err != nil || len(meta) != 1 || meta[0].DatabaseTypeName() != "TIMESTAMPTZ" || meta[0].ScanType() != reflect.TypeFor[time.Time]() {
			t.Fatal("infinity metadata", meta, err)
		}
		if !rows.Next() {
			rows.Close()
			t.Fatal("first infinity", rows.Err())
		}
		var first any
		if err := rows.Scan(&first); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		bytes, ok := first.([]byte)
		if !ok || string(bytes) != "infinity" {
			rows.Close()
			t.Fatal(first)
		}
		bytes[0] = 'X'
		if !rows.Next() {
			rows.Close()
			t.Fatal("second infinity", rows.Err())
		}
		var second any
		if err := rows.Scan(&second); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		fresh, ok := second.([]byte)
		if !ok || string(fresh) != "infinity" || string(bytes) != "Xnfinity" {
			rows.Close()
			t.Fatal(first, second)
		}
		if rows.Next() || rows.Err() != nil {
			rows.Close()
			t.Fatal("infinity cardinality", rows.Err())
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		record.mu.Lock()
		if len(record.operations) != 1 {
			record.mu.Unlock()
			t.Fatal("infinity query recording", len(record.operations))
		}
		op := record.operations[0]
		record.mu.Unlock()
		if !op.Closed || op.CloseErr != nil || !reflect.DeepEqual(op.Columns, []string{"z"}) || len(op.Rows) != 2 {
			t.Fatal("infinity consumed output/closure", op)
		}
		for _, row := range op.Rows {
			referenceAssertRawValue(t, false, "TIMESTAMPTZ", row[0])
			if referenceNormalize(row[0]) != "infinity" {
				t.Fatal("recorded infinity aliased exported bytes", row)
			}
		}
		// Mutating an exported infinity must not alter the stored value.
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SELECT z::TEXT AS z FROM tz_witness WHERE id=2", []string{"z"}, []string{"TEXT"}, [][]any{{"infinity"}}))
	}
}
