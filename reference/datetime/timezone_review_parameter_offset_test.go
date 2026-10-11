package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

var timezoneParameterOffsetCases = []struct {
	name                 string
	east                 int
	text, truncated, utc string
}{
	{"positive_seconds", 30, "05:34:17.123456+00:00:30", "05:34:17.123456+00", "05:33:47.123456+00"},
	{"negative_seconds", -30, "05:34:17.123456-00:00:30", "05:34:17.123456+00", "05:34:47.123456+00"},
	{"positive_components", 7384, "05:34:17.123456+02:03:04", "05:34:17.123456+02:03", "03:31:13.123456+00"},
	{"negative_components", -7384, "05:34:17.123456-02:03:04", "05:34:17.123456-02:03", "07:37:21.123456+00"},
}

func TestReferenceTimezoneReviewParameterOffset(t *testing.T) {
	p := fixture.TimezoneReferencePair(t)
	p.Step(t, fixture.Exec("CREATE TABLE tz_parameter_import(id INTEGER,q TIMETZ(6))", 0))
	// Independent constants retain the civil clock and subtract the complete
	// east offset for UTC conversion, never an offset derived by a backend.
	query := "SELECT ($1::TIMETZ)::TEXT AS q,($1::TIMETZ AT TIME ZONE 'UTC')::TEXT AS utc,$1::TIMETZ=$2::TIMETZ AS equal,$1::TIMETZ<$2::TIMETZ AS less,$1::TIMETZ=$3::TIMETZ AS exact FROM tz_witness WHERE id=1"
	for i, tc := range timezoneParameterOffsetCases {
		t.Run(tc.name, func(t *testing.T) {
			value := time.Date(2004, 7, 1, 5, 34, 17, 123456000, time.FixedZone("source", tc.east))
			s := fixture.Select(query, []string{"q", "utc", "equal", "less", "exact"}, []string{"TEXT", "TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{tc.text, tc.utc, false, tc.east > 0, true}}, value, tc.truncated, tc.text)
			if tc.east != -30 {
				timezoneBoth(t, p, s)
			} else {
				// pq v1.12.3 pqtime.Format prints a minute-only Go offset, then
				// appends abs(offset%60). Its -30s bind becomes +00:00:30.
				// Assert this adapter discrepancy; do not corrupt native values
				// to mimic it or attribute it to the PostgreSQL server.
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						step := s
						if backend.PG {
							step.Rows = [][]any{{"05:34:17.123456+00:00:30", "05:33:47.123456+00", false, true, false}}
						}
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(step))
					})
				}
				// Text bypasses pq's encoder and proves PostgreSQL itself accepts
				// the same negative sub-minute offset preserved by native import.
				timezoneBoth(t, p, fixture.Select(query, s.Columns, s.Types, s.Rows, tc.text, tc.truncated, tc.text))
			}
			for mode := range 2 {
				id := int64(10 + 2*i + mode)
				insert := fixture.Exec("INSERT INTO tz_parameter_import VALUES($1,$2)", 1, id, value)
				insert.Prepared = mode == 1
				p.Step(t, insert)
			}
			stored := fixture.Select("SELECT q::TEXT AS q,(q AT TIME ZONE 'UTC')::TEXT AS utc FROM tz_parameter_import WHERE id>=$1 AND id<=$2 ORDER BY id", []string{"q", "utc"}, []string{"TEXT", "TEXT"}, [][]any{{tc.text, tc.utc}, {tc.text, tc.utc}}, int64(10+2*i), int64(11+2*i))
			if tc.east != -30 {
				timezoneBoth(t, p, stored)
			} else {
				fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, stored)
				stored.Rows = [][]any{{"05:34:17.123456+00:00:30", "05:33:47.123456+00"}, {"05:34:17.123456+00:00:30", "05:33:47.123456+00"}}
				if p.Postgres != nil {
					fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, stored)
				}
			}
			// Raw pq results are time.Time rather than native strings. Check
			// their civil fields and full offset independently of text parity.
			if p.Postgres == nil {
				return
			}
			for _, prepared := range []bool{false, true} {
				t.Run(map[bool]string{false: "pq_raw_direct", true: "pq_raw_prepared"}[prepared], func(t *testing.T) {
					var rows *sql.Rows
					var err error
					rawQuery := "SELECT $1::TIMETZ AS q FROM tz_witness WHERE id=1"
					if prepared {
						stmt, prepareErr := p.Postgres.PrepareContext(p.Ctx, rawQuery)
						if prepareErr != nil {
							t.Fatal(prepareErr)
						}
						defer stmt.Close()
						rows, err = stmt.QueryContext(p.Ctx, value)
					} else {
						rows, err = p.Postgres.QueryContext(p.Ctx, rawQuery, value)
					}
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					meta, err := rows.ColumnTypes()
					if err != nil || len(meta) != 1 || meta[0].DatabaseTypeName() != "TIMETZ" {
						t.Fatal("pq TIMETZ metadata", meta, err)
					}
					if !rows.Next() {
						t.Fatal("missing pq TIMETZ", rows.Err())
					}
					var clock time.Time
					if err := rows.Scan(&clock); err != nil {
						t.Fatal(err)
					}
					wantEast := tc.east
					if wantEast == -30 {
						wantEast = 30
					}
					_, east := clock.Zone()
					if clock.Hour() != 5 || clock.Minute() != 34 || clock.Second() != 17 || clock.Nanosecond() != 123456000 || east != wantEast {
						t.Fatal("pq civil clock and fixed offset", clock, east, wantEast)
					}
					if rows.Next() || rows.Err() != nil {
						t.Fatal("pq row cardinality", rows.Err())
					}
				})
			}
		})
	}
}
