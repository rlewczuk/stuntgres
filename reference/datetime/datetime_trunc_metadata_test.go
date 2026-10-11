package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func datetimeTruncMetadata(t *testing.T, pg bool, columns []*sql.ColumnType, names, types []string) {
	t.Helper()
	if len(columns) != len(types) {
		t.Fatalf("metadata count %d want %d", len(columns), len(types))
	}
	for i, column := range columns {
		scan := reflect.TypeFor[time.Time]()
		if types[i] == "INTERVAL" {
			scan = reflect.TypeFor[string]()
			if pg {
				scan = reflect.TypeFor[any]()
			}
		}
		precision, scale, decimal := column.DecimalSize()
		length, hasLength := column.Length()
		nullable, known := column.Nullable()
		// Computed temporal usages are unrestricted; neither adapter exposes
		// numeric precision, a fixed string length or known nullability.
		if column.Name() != names[i] || column.DatabaseTypeName() != types[i] || column.ScanType() != scan || precision != 0 || scale != 0 || decimal || length != 0 || hasLength || nullable || known {
			t.Fatalf("%s metadata name=%s type=%s scan=%v decimal=(%d,%d,%t) length=(%d,%t) nullable=(%t,%t)", types[i], column.Name(), column.DatabaseTypeName(), column.ScanType(), precision, scale, decimal, length, hasLength, nullable, known)
		}
	}
}

func datetimeTruncPublicMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	names := []string{"stamp", "instant", "span"}
	types := []string{"TIMESTAMP", "TIMESTAMPTZ", "INTERVAL"}
	for _, tc := range []struct {
		stamp, instant, span, suffix string
		rows                         [][]any
	}{
		{"TIMESTAMP(2) '2001-02-16 12:34:56.12'", "TIMESTAMPTZ(2) '2001-02-16 12:34:56.12+00'", "INTERVAL(2) '1 day 12:34:56.12'", "", [][]any{{datetimeTruncInstant("2001-02-16T12:34:56.12Z"), datetimeTruncInstant("2001-02-16T12:34:56.12Z"), "1 day 12:34:56.12"}}},
		{"NULL::timestamp(2)", "NULL::timestamptz(2)", "NULL::interval(2)", "", [][]any{{nil, nil, nil}}},
		{"TIMESTAMP(2) '2001-02-16 12:34:56.12'", "TIMESTAMPTZ(2) '2001-02-16 12:34:56.12+00'", "INTERVAL(2) '1 day 12:34:56.12'", " WHERE false", nil},
	} {
		q := "SELECT date_trunc('microseconds'," + tc.stamp + ") AS stamp,date_trunc('microseconds'," + tc.instant + ",'UTC') AS instant,date_trunc('microseconds'," + tc.span + ") AS span" + tc.suffix
		for _, prepared := range []bool{false, true} {
			var retained []*sql.ColumnType
			s := fixture.Select(q, names, types, tc.rows)
			s.Prepared = prepared
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				var err error
				retained, err = rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				datetimeTruncMetadata(t, b.PG, retained, names, types)
			}
			s.CheckScan = func(t *testing.T, rows *sql.Rows, index int) {
				var stamp, instant, span any
				if err := rows.Scan(&stamp, &instant, &span); err != nil {
					t.Fatal(err)
				}
				for i, v := range []any{stamp, instant, span} {
					fixture.AssertRawValue(t, b.PG, types[i], v)
				}
				if !reflect.DeepEqual([]any{fixture.Normalize(stamp), fixture.Normalize(instant), fixture.Normalize(span)}, tc.rows[index]) {
					t.Fatalf("raw temporal result %#v %#v %#v", stamp, instant, span)
				}
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			datetimeTruncMetadata(t, b.PG, retained, names, types)
		}
	}
	if b.PG {
		return
	}
	// Capture computed descriptors before replacement and assert them afterwards,
	// independently of the adapters' table/view-column nullable declarations.
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE dm_source(v timestamp(2),t timestamptz(2),i interval(2))", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO dm_source VALUES(TIMESTAMP '2001-02-16 12:34:56.12',TIMESTAMPTZ '2001-02-16 12:34:56.12+00',INTERVAL '1 day 12:34:56.12')", 1))
	q := "SELECT date_trunc('microseconds',v) AS stamp,date_trunc('microseconds',t,'UTC') AS instant,date_trunc('microseconds',i) AS span FROM dm_source"
	rows, err := p.Native.QueryContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	retained, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	datetimeTruncMetadata(t, false, retained, names, types)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	datetimeTruncMetadata(t, false, retained, names, types)
	if !rows.Next() {
		t.Fatalf("retained temporal row: %v", rows.Err())
	}
	var stamp, instant, span any
	if err := rows.Scan(&stamp, &instant, &span); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual([]any{fixture.Normalize(stamp), fixture.Normalize(instant), fixture.Normalize(span)}, []any{datetimeTruncInstant("2001-02-16T12:34:56.12Z"), datetimeTruncInstant("2001-02-16T12:34:56.12Z"), "1 day 12:34:56.12"}) {
		t.Fatalf("retained raw row %#v %#v %#v", stamp, instant, span)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("retained temporal tail")
	}
	rows.Close()
	datetimeTruncMetadata(t, false, retained, names, types)
}

func TestReferenceDatetimeTruncPublicMetadata(t *testing.T) {
	datetimeTruncScenario(t, datetimeTruncPublicMetadata)
}
