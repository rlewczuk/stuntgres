package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceTimezoneAbbreviationsMetadataAtomicity(t *testing.T) {
	timezoneAbbreviationsScenario(t, func(p *fixture.Pair, x fixture.Executor, record *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, x, record, s) }
		run(fixture.Exec("SET timezone_abbreviations='India'", 0))
		query := "SELECT DATE '2004-07-01' AS d,TIME '12:00 IST' AS t,TIMESTAMP '2004-07-01 12:00 IST' AS c,TIMESTAMPTZ '2004-07-01 12:00 IST' AS z,INTERVAL '1 day' AS i,TIMETZ '12:00 IST' AS q FROM tz_witness"
		rows, err := x.QueryContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		timezoneAbbreviationsMetadata(t, rows, pg)
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		var d, clock, civil, instant, interval, timetz any
		if err := rows.Scan(&d, &clock, &civil, &instant, &interval, &timetz); err != nil {
			t.Fatal(err)
		}
		if got, ok := civil.(time.Time); !ok || !got.Equal(time.Date(2004, 7, 1, 12, 0, 0, 0, time.UTC)) {
			t.Fatal(civil)
		}
		if got, ok := instant.(time.Time); !ok || !got.Equal(time.Date(2004, 7, 1, 6, 30, 0, 0, time.UTC)) {
			t.Fatal(instant)
		}
		if pg {
			if _, ok := d.(time.Time); !ok {
				t.Fatal(d)
			}
			if _, ok := clock.(time.Time); !ok {
				t.Fatal(clock)
			}
			if _, ok := interval.([]byte); !ok {
				t.Fatal(interval)
			}
			got, ok := timetz.(time.Time)
			if !ok {
				t.Fatal(timetz)
			}
			_, east := got.Zone()
			if got.Hour() != 12 || east != 19800 {
				t.Fatal(got)
			}
		} else if d != "2004-07-01" || clock != "12:00:00" || interval != "1 day" || timetz != "12:00:00+05:30" {
			t.Fatal(d, clock, interval, timetz)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal(rows.Err())
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		nulls := fixture.Select("SELECT NULL::DATE AS d,NULL::TIME AS t,NULL::TIMESTAMP AS c,NULL::TIMESTAMPTZ AS z,NULL::INTERVAL AS i,NULL::TIMETZ AS q FROM tz_witness", []string{"d", "t", "c", "z", "i", "q"}, []string{"DATE", "TIME", "TIMESTAMP", "TIMESTAMPTZ", "INTERVAL", "TIMETZ"}, [][]any{{nil, nil, nil, nil, nil, nil}})
		nulls.CheckMetadata = func(t *testing.T, rows *sql.Rows) { timezoneAbbreviationsMetadata(t, rows, pg) }
		for _, prepared := range []bool{false, true} {
			nulls.Prepared = prepared
			run(nulls)
		}
		for _, tc := range []struct {
			name string
			oid  int64
		}{{"date", 1082}, {"time", 1083}, {"timestamp", 1114}, {"timestamptz", 1184}, {"interval", 1186}, {"timetz", 1266}} {
			run(fixture.Select("SELECT oid::BIGINT AS oid FROM pg_catalog.pg_type WHERE typname='"+tc.name+"'", []string{"oid"}, []string{"INT8"}, [][]any{{tc.oid}}))
		}
		run(fixture.Exec("UPDATE tz_witness SET z='2004-07-01 12:00 IST' WHERE id=1", 1))
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		if _, err := x.ExecContext(canceled, "SET timezone_abbreviations='Australia'"); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		run(timezoneAbbreviationsShow("India"))
		query = "INSERT INTO tz_witness(z) VALUES($1::TEXT::TIMESTAMPTZ),($2::TEXT::TIMESTAMPTZ)"
		s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22007", Message: `invalid input syntax for type timestamp with time zone: "nonsense"`}, "2005-01-01 12:00 IST", "nonsense")
		s.ErrorStage = "execute"
		for _, prepared := range []bool{false, true} {
			s.Prepared = prepared
			run(s)
			run(fixture.Select("SELECT COUNT(*) AS n FROM tz_witness", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
			run(timezoneTextStep("z", "2004-07-01 06:30:00+00"))
		}
	})
}

// These computed temporal projections have unknown nullability on both public
// adapters. Neither adapter advertises storage length or decimal precision.
// Inspect complete value/known pairs, not only their known bits.
func timezoneAbbreviationsMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	meta, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"d", "t", "c", "z", "i", "q"}
	types := []string{"DATE", "TIME", "TIMESTAMP", "TIMESTAMPTZ", "INTERVAL", "TIMETZ"}
	if len(meta) != len(names) {
		t.Fatalf("temporal descriptor count %d, want %d", len(meta), len(names))
	}
	for i, m := range meta {
		want := reflect.TypeFor[string]()
		if i == 2 || i == 3 || pg && (i == 0 || i == 1 || i == 5) {
			want = reflect.TypeFor[time.Time]()
		}
		if pg && i == 4 {
			want = reflect.TypeFor[any]()
		}
		if m.Name() != names[i] || m.DatabaseTypeName() != types[i] || m.ScanType() != want {
			t.Fatalf("temporal descriptor %d: name=%q type=%s scan=%v, want %q/%s/%v", i, m.Name(), m.DatabaseTypeName(), m.ScanType(), names[i], types[i], want)
		}
		if nullable, known := m.Nullable(); nullable || known {
			t.Fatalf("%s nullable=%t/%t, want false/false", names[i], nullable, known)
		}
		if length, known := m.Length(); length != 0 || known {
			t.Fatalf("%s length=%d/%t, want 0/false", names[i], length, known)
		}
		if precision, scale, known := m.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatalf("%s decimal=%d/%d/%t, want 0/0/false", names[i], precision, scale, known)
		}
	}
}
