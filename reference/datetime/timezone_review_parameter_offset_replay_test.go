package reference_test

import (
	"bytes"
	"context"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceTimezoneReviewParameterOffsetCapture(t *testing.T) {
	p := fixture.TimezoneReferencePair(t)
	c, err := p.Memory.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	query := "SELECT $1::TIMETZ AS q,($1::TIMETZ)::TEXT AS text,($1::TIMETZ AT TIME ZONE 'UTC')::TEXT AS utc,$1::TIMETZ=$2::TIMETZ AS equal,$1::TIMETZ<$2::TIMETZ AS less,$1::TIMETZ=$3::TIMETZ AS exact FROM tz_witness WHERE id=1"
	held := timezoneHeld(t, p, c, query)
	defer held.Statement.Close()
	for _, zone := range []string{"UTC", "Asia/Tokyo"} {
		for _, tc := range timezoneParameterOffsetCases {
			for _, prepared := range []bool{false, true} {
				fixture.RunStep(t, p.Ctx, c, p.MemoryRecord, fixture.Exec("SET TIME ZONE '"+zone+"'", 0))
				value := time.Date(2004, 7, 1, 5, 34, 17, 123456000, time.FixedZone("source", tc.east))
				var x fixture.Executor = c
				if prepared {
					x = held
				}
				rows, err := x.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, p.MemoryRecord), query, value, tc.truncated, tc.text)
				if err != nil {
					t.Fatal(err)
				}
				meta, err := rows.ColumnTypes()
				if err != nil || len(meta) != 6 || meta[0].ScanType() != reflect.TypeFor[string]() {
					rows.Close()
					t.Fatal(meta, err)
				}
				fixture.RunStep(t, p.Ctx, c, p.MemoryRecord, fixture.Exec("SET TIME ZONE 'America/New_York'", 0))
				s := fixture.Select(query, []string{"q", "text", "utc", "equal", "less", "exact"}, []string{"TIMETZ", "TEXT", "TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{tc.text, tc.text, tc.utc, false, tc.east > 0, true}})
				fixture.RunStep(t, p.Ctx, timezoneAdmittedRows{Executor: c, rows: rows}, p.MemoryRecord, s)
			}
		}
	}
}

func TestReferenceTimezoneReviewParameterOffsetReplay(t *testing.T) {
	p := fixture.TimezoneReferencePair(t)
	// Native host round trips preserve offsets encoded correctly by the native
	// adapter; the canonical reference test retains pq's independent -30s bug.
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Exec("CREATE TABLE tz_parameter_import(id INTEGER,q TIMETZ(6))", 0))
	query := "INSERT INTO tz_parameter_import VALUES($1,$2)"
	held := timezoneHeld(t, p, p.Memory, query)
	want := make([][]any, 0, 8)
	for i, tc := range timezoneParameterOffsetCases {
		value := time.Date(2004, 7, 1, 5, 34, 17, 123456000, time.FixedZone("source", tc.east))
		for mode := range 2 {
			id := int64(2*i + mode + 1)
			var x fixture.Executor = p.Memory
			if mode == 1 {
				x = held
			}
			fixture.RunStep(t, p.Ctx, x, p.MemoryRecord, fixture.Exec(query, 1, id, value))
			want = append(want, []any{id, tc.text, tc.utc})
		}
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	native, target, record := timezoneDestination(t)
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, target, record, s) }
	run(fixture.Exec("SET TIME ZONE 'Asia/Tokyo'", 0))
	run(fixture.Exec("SET DateStyle='German, DMY'", 0))
	if err := stuntgres.Restore(p.Ctx, native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	run(fixture.Select("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"Asia/Tokyo"}}))
	run(fixture.Select("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"German, DMY"}}))
	run(fixture.Select("SELECT id FROM tz_parameter_import ORDER BY tz_parameter_import.q,id", []string{"id"}, []string{"INT4"}, [][]any{{int64(5)}, {int64(6)}, {int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(7)}, {int64(8)}}))
	output := fixture.Select("SELECT id,q,(q AT TIME ZONE 'UTC')::TEXT AS utc FROM tz_parameter_import ORDER BY id", []string{"id", "q", "utc"}, []string{"INT4", "TIMETZ", "TEXT"}, want)
	run(output)
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, output)
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, native, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal("redump mismatch", err)
	}
	if p.Postgres != nil {
		// Replay the unchanged owner-bearing stream, not a reconstructed/text-only
		// substitute. This also proves PostgreSQL accepts negative subminute offsets.
		if _, err := fixture.Admin.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.Admin.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal(err)
		}
		s := fixture.Select("SELECT id,q::TEXT AS q,(q AT TIME ZONE 'UTC')::TEXT AS utc FROM tz_parameter_import ORDER BY id", []string{"id", "q", "utc"}, []string{"INT4", "TEXT", "TEXT"}, want)
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
	}
}
