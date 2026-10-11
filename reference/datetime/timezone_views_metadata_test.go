package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func timezoneViewsMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	meta, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range meta {
		wantScan := reflect.TypeFor[string]()
		if m.DatabaseTypeName() == "BOOL" {
			wantScan = reflect.TypeFor[bool]()
		}
		if pg && m.DatabaseTypeName() == "INTERVAL" {
			wantScan = reflect.TypeFor[any]()
		}
		if m.ScanType() != wantScan {
			t.Errorf("%s scan=%v want %v", m.Name(), m.ScanType(), wantScan)
		}
		wantNullable, wantKnown := true, true
		if pg {
			wantNullable, wantKnown = false, false
		}
		if value, known := m.Nullable(); value != wantNullable || known != wantKnown {
			t.Errorf("%s nullable=%t/%t want %t/%t", m.Name(), value, known, wantNullable, wantKnown)
		}
		// Both public adapters expose unrestricted TEXT as MaxInt64/true.
		// These adapter lengths are not server attribute typmods, which are
		// independently asserted below.
		wantLength, wantLengthKnown := int64(0), false
		if m.DatabaseTypeName() == "TEXT" {
			wantLength, wantLengthKnown = math.MaxInt64, true
		}
		if value, known := m.Length(); value != wantLength || known != wantLengthKnown {
			t.Errorf("%s length=%d/%t want %d/%t", m.Name(), value, known, wantLength, wantLengthKnown)
		}
		if precision, scale, known := m.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Errorf("%s decimal=%d/%d/%t want 0/0/false", m.Name(), precision, scale, known)
		}
	}
}

func TestReferenceTimezoneViewsMetadata(t *testing.T) {
	timezoneViewsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, empty := range []bool{false, true} {
			name, abbrev := "UTC", "UTC"
			names, abbrevs := [][]any{{"UTC", "UTC", "00:00:00", false}}, [][]any{{"UTC", "00:00:00", false}}
			if empty {
				name, abbrev, names, abbrevs = "No/Such_Zone", "NoSuchAbbreviation", [][]any{}, [][]any{}
			}
			for _, s := range []fixture.Step{
				timezoneViewsNames("SELECT * FROM pg_timezone_names WHERE name=$1::text", names, name),
				timezoneViewsAbbrevs("SELECT * FROM pg_timezone_abbrevs WHERE abbrev=$1::text", abbrevs, abbrev),
			} {
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { timezoneViewsMetadata(t, rows, b.PG) }
				run(s)
				run(fixture.Prepared(s))
			}
		}
		run(fixture.Select("SELECT relname,relkind,relnatts FROM pg_catalog.pg_class WHERE relname='pg_timezone_names' OR relname='pg_timezone_abbrevs' ORDER BY relname", []string{"relname", "relkind", "relnatts"}, []string{"NAME", "CHAR", "INT2"}, [][]any{{"pg_timezone_abbrevs", "v", int64(3)}, {"pg_timezone_names", "v", int64(4)}}))
		// Supplemental release attribute research only: native pg_attribute is
		// outside this part. Public ColumnTypes above remains shared coverage.
		if b.PG {
			for _, tc := range []struct {
				view string
				rows [][]any
			}{
				{"pg_timezone_names", [][]any{{"name", int64(25), int64(-1), false}, {"abbrev", int64(25), int64(-1), false}, {"utc_offset", int64(1186), int64(-1), false}, {"is_dst", int64(16), int64(-1), false}}},
				{"pg_timezone_abbrevs", [][]any{{"abbrev", int64(25), int64(-1), false}, {"utc_offset", int64(1186), int64(-1), false}, {"is_dst", int64(16), int64(-1), false}}},
			} {
				run(fixture.Select("SELECT attname::text AS name,atttypid::bigint AS type_oid,atttypmod::bigint AS typmod,attnotnull FROM pg_catalog.pg_attribute WHERE attrelid='pg_catalog."+tc.view+"'::regclass AND attnum>0 ORDER BY attnum", []string{"name", "type_oid", "typmod", "attnotnull"}, []string{"TEXT", "INT8", "INT8", "BOOL"}, tc.rows))
			}
		}
	})
}
