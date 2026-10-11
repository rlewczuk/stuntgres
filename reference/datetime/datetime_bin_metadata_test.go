package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func datetimeBinMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	names, types := []string{"date_bin", "instant"}, []string{"TIMESTAMP", "TIMESTAMPTZ"}
	for _, tc := range []struct {
		source, suffix string
		rows           [][]any
	}{
		{"'2000-01-01 00:00:00.12'", "", [][]any{{datetimeTruncInstant("2000-01-01T00:00:00.119Z"), datetimeTruncInstant("2000-01-01T00:00:00.119Z")}}},
		{"NULL", "", [][]any{{nil, nil}}},
		{"'2000-01-01 00:00:00.12'", " WHERE false", nil},
		{"'infinity'", "", [][]any{{"infinity", "infinity"}}},
		{"'-infinity'", "", [][]any{{"-infinity", "-infinity"}}},
	} {
		q := "SELECT date_bin('7 milliseconds'," + tc.source + "::timestamp(2),TIMESTAMP(2) '2000-01-01'),date_bin('7 milliseconds'," + tc.source + "::timestamptz(2),TIMESTAMPTZ(2) '2000-01-01+00') AS instant" + tc.suffix
		s := fixture.Select(q, names, types, tc.rows)
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			columns, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			datetimeTruncMetadata(t, b.PG, columns, names, types)
		}
		s.CheckScan = func(t *testing.T, rows *sql.Rows, index int) {
			var stamp, instant any
			if err := rows.Scan(&stamp, &instant); err != nil {
				t.Fatal(err)
			}
			for i, value := range []any{stamp, instant} {
				fixture.AssertRawValue(t, b.PG, types[i], value)
			}
			if !reflect.DeepEqual([]any{fixture.Normalize(stamp), fixture.Normalize(instant)}, tc.rows[index]) {
				t.Fatalf("bin raw row: %#v %#v", stamp, instant)
			}
		}
		datetimeExtractBoth(t, p, b, c, s)
	}
	if b.PG {
		return
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE db_meta(s timestamp(2),z timestamptz(2))", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO db_meta VALUES(TIMESTAMP '2000-01-01 00:00:00.12',TIMESTAMPTZ '2000-01-01 00:00:00.12+00')", 1))
	q := "SELECT date_bin('7 milliseconds',s,TIMESTAMP '2000-01-01'),date_bin('7 milliseconds',z,TIMESTAMPTZ '2000-01-01+00') AS instant FROM db_meta"
	old, err := p.Native.QueryContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	columns, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	datetimeTruncMetadata(t, false, columns, names, types)
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
	datetimeTruncMetadata(t, false, columns, names, types)
	if !old.Next() {
		t.Fatalf("retained bin row: %v", old.Err())
	}
	var stamp, instant any
	if err := old.Scan(&stamp, &instant); err != nil {
		t.Fatal(err)
	}
	want := datetimeTruncInstant("2000-01-01T00:00:00.119Z")
	if !reflect.DeepEqual([]any{fixture.Normalize(stamp), fixture.Normalize(instant)}, []any{want, want}) {
		t.Fatalf("retained bin row: %#v %#v", stamp, instant)
	}
	if old.Next() || old.Err() != nil {
		t.Fatalf("retained bin tail: %v", old.Err())
	}
	old.Close()
	datetimeTruncMetadata(t, false, columns, names, types)
	datetimeExtractBoth(t, p, b, c, fixture.Select(q, names, types, [][]any{{want, want}}))
}

func TestReferenceDatetimeBinMetadata(t *testing.T) {
	datetimeBinScenario(t, datetimeBinMetadata)
}
