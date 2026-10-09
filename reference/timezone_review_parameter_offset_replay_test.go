package reference_test

import (
	"bytes"
	"context"
	"reflect"
	"stuntgres"
	"testing"
	"time"
)

func TestReferenceTimezoneReviewParameterOffsetCapture(t *testing.T) {
	p := timezoneReferencePair(t)
	c, err := p.memory.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	query := "SELECT $1::TIMETZ AS q,($1::TIMETZ)::TEXT AS text,($1::TIMETZ AT TIME ZONE 'UTC')::TEXT AS utc,$1::TIMETZ=$2::TIMETZ AS equal,$1::TIMETZ<$2::TIMETZ AS less,$1::TIMETZ=$3::TIMETZ AS exact FROM tz_witness WHERE id=1"
	held := timezoneHeld(t, p, c, query)
	defer held.statement.Close()
	for _, zone := range []string{"UTC", "Asia/Tokyo"} {
		for _, tc := range timezoneParameterOffsetCases {
			for _, prepared := range []bool{false, true} {
				referenceRunStep(t, p.ctx, c, p.memoryRecord, referenceExec("SET TIME ZONE '"+zone+"'", 0))
				value := time.Date(2004, 7, 1, 5, 34, 17, 123456000, time.FixedZone("source", tc.east))
				var x referenceExecutor = c
				if prepared {
					x = held
				}
				rows, err := x.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, p.memoryRecord), query, value, tc.truncated, tc.text)
				if err != nil {
					t.Fatal(err)
				}
				meta, err := rows.ColumnTypes()
				if err != nil || len(meta) != 6 || meta[0].ScanType() != reflect.TypeFor[string]() {
					rows.Close()
					t.Fatal(meta, err)
				}
				referenceRunStep(t, p.ctx, c, p.memoryRecord, referenceExec("SET TIME ZONE 'America/New_York'", 0))
				s := referenceSelect(query, []string{"q", "text", "utc", "equal", "less", "exact"}, []string{"TIMETZ", "TEXT", "TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{tc.text, tc.text, tc.utc, false, tc.east > 0, true}})
				referenceRunStep(t, p.ctx, timezoneAdmittedRows{referenceExecutor: c, rows: rows}, p.memoryRecord, s)
			}
		}
	}
}

func TestReferenceTimezoneReviewParameterOffsetReplay(t *testing.T) {
	p := timezoneReferencePair(t)
	// Native host round trips preserve offsets encoded correctly by the native
	// adapter; the canonical reference test retains pq's independent -30s bug.
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("CREATE TABLE tz_parameter_import(id INTEGER,q TIMETZ(6))", 0))
	query := "INSERT INTO tz_parameter_import VALUES($1,$2)"
	held := timezoneHeld(t, p, p.memory, query)
	want := make([][]any, 0, 8)
	for i, tc := range timezoneParameterOffsetCases {
		value := time.Date(2004, 7, 1, 5, 34, 17, 123456000, time.FixedZone("source", tc.east))
		for mode := range 2 {
			id := int64(2*i + mode + 1)
			var x referenceExecutor = p.memory
			if mode == 1 {
				x = held
			}
			referenceRunStep(t, p.ctx, x, p.memoryRecord, referenceExec(query, 1, id, value))
			want = append(want, []any{id, tc.text, tc.utc})
		}
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	native, target, record := timezoneDestination(t)
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, target, record, s) }
	run(referenceExec("SET TIME ZONE 'Asia/Tokyo'", 0))
	run(referenceExec("SET DateStyle='German, DMY'", 0))
	if err := stuntgres.Restore(p.ctx, native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	run(referenceSelect("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"Asia/Tokyo"}}))
	run(referenceSelect("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"German, DMY"}}))
	run(referenceSelect("SELECT id FROM tz_parameter_import ORDER BY tz_parameter_import.q,id", []string{"id"}, []string{"INT4"}, [][]any{{int64(5)}, {int64(6)}, {int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(7)}, {int64(8)}}))
	output := referenceSelect("SELECT id,q,(q AT TIME ZONE 'UTC')::TEXT AS utc FROM tz_parameter_import ORDER BY id", []string{"id", "q", "utc"}, []string{"INT4", "TIMETZ", "TEXT"}, want)
	run(output)
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, output)
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, native, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal("redump mismatch", err)
	}
	if p.postgres != nil {
		// Replay the unchanged owner-bearing stream, not a reconstructed/text-only
		// substitute. This also proves PostgreSQL accepts negative subminute offsets.
		if _, err := referenceAdmin.ExecContext(p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"); err != nil {
			t.Fatal(err)
		}
		if _, err := referenceAdmin.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatal(err)
		}
		s := referenceSelect("SELECT id,q::TEXT AS q,(q AT TIME ZONE 'UTC')::TEXT AS utc FROM tz_parameter_import ORDER BY id", []string{"id", "q", "utc"}, []string{"INT4", "TEXT", "TEXT"}, want)
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
	}
}
