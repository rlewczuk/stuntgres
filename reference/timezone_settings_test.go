package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"testing"
	"time"
)

func TestReferenceTimezonePooledIsolation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := timezoneReferencePair(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					backend.db.SetMaxOpenConns(2)
					a, err := backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer a.Close()
					b, err := backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer b.Close()
					run := func(conn *sql.Conn, s referenceStep) {
						s.prepared = prepared
						referenceRunStep(t, p.ctx, conn, backend.recorder, s)
					}
					defaultZone := timezoneDefaultZone(t, p, a, backend.pg)
					run(a, referenceExec("SET TIME ZONE 'UTC'", 0))
					run(b, referenceExec("SET TIME ZONE 'America/New_York'", 0))
					run(a, referenceExec("SET DateStyle='ISO,MDY'", 0))
					run(b, referenceExec("SET DateStyle='ISO,DMY'", 0))
					run(a, referenceExec("UPDATE tz_witness SET z='2004-10-19 10:23:54+02',q='05:34:17-05',d='2004-10-19',c='2004-10-19 00:00' WHERE id=1", 1))
					for range 2 {
						run(a, referenceSelect("SELECT z::TEXT AS z,z=TIMESTAMPTZ '2004-10-19 08:23:54+00' AS same,q::TEXT AS q FROM tz_witness", []string{"z", "same", "q"}, []string{"TEXT", "BOOL", "TEXT"}, [][]any{{"2004-10-19 08:23:54+00", true, "05:34:17-05"}}))
						run(b, referenceSelect("SELECT z::TEXT AS z,z=TIMESTAMPTZ '2004-10-19 08:23:54+00' AS same,q::TEXT AS q FROM tz_witness", []string{"z", "same", "q"}, []string{"TEXT", "BOOL", "TEXT"}, [][]any{{"2004-10-19 04:23:54-04", true, "05:34:17-05"}}))
					}
					run(b, referenceSelect("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"ISO, DMY"}}))
					run(b, referenceSelect("SELECT TIMESTAMPTZ '2004-07-01 12:00' AT TIME ZONE 'UTC' AS z FROM tz_witness", []string{"z"}, []string{"TIMESTAMP"}, [][]any{{time.Date(2004, 7, 1, 16, 0, 0, 0, time.UTC)}}))
					run(b, referenceExec("SET DateStyle='SQL,DMY'", 0))
					for range 3 {
						run(a, referenceSelect("SELECT z::TEXT AS v,z=TIMESTAMPTZ '2004-10-19 08:23:54+00' AS same FROM tz_witness", []string{"v", "same"}, []string{"TEXT", "BOOL"}, [][]any{{"2004-10-19 08:23:54+00", true}}))
						run(b, referenceSelect("SELECT z::TEXT AS v,z=TIMESTAMPTZ '2004-10-19 08:23:54+00' AS same FROM tz_witness", []string{"v", "same"}, []string{"TEXT", "BOOL"}, [][]any{{"19/10/2004 04:23:54 EDT", true}}))
						run(a, referenceSelect("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"ISO, MDY"}}))
						run(b, referenceSelect("SHOW TIME ZONE", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"America/New_York"}}))
					}
					// Returning A while B remains borrowed forces reborrow of A's physical session.
					if err := a.Close(); err != nil {
						t.Fatal(err)
					}
					a, err = backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer a.Close()
					run(a, referenceSelect("SHOW TIME ZONE", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"UTC"}}))
					run(a, referenceExec("SET TIME ZONE 'UTC-2'", 0))
					if err := a.Raw(func(any) error { return driver.ErrBadConn }); err != driver.ErrBadConn {
						t.Fatal(err)
					}
					a, err = backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer a.Close()
					run(a, referenceSelect("SHOW TIME ZONE", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{defaultZone}}))
					run(a, referenceExec("SET TIME ZONE 'UTC'", 0))
					run(a, referenceSelect("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"ISO, MDY"}}))
					run(a, referenceSelect("SELECT COUNT(*) AS n FROM tz_witness", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
					// Public native RowResult must retain its execution-captured output zone.
					// pq buffers protocol values; keep the same exact SQL TEXT assertion there.
					rows, err := a.QueryContext(p.ctx, "SELECT z FROM tz_witness")
					if err != nil {
						t.Fatal(err)
					}
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					if !backend.pg {
						rows, err := a.QueryContext(p.ctx, "SELECT z FROM tz_witness")
						if err != nil {
							t.Fatal(err)
						}
						defer rows.Close()
						if _, err := a.ExecContext(p.ctx, "SET TIME ZONE 'UTC-2'"); err != nil {
							t.Fatal(err)
						}
						if !rows.Next() {
							t.Fatal(rows.Err())
						}
						var z time.Time
						if err := rows.Scan(&z); err != nil {
							t.Fatal(err)
						}
						_, offset := z.Zone()
						if offset != 0 || !z.Equal(time.Date(2004, 10, 19, 8, 23, 54, 0, time.UTC)) {
							t.Fatal(z, offset)
						}
						rows.Close()
						run(a, timezoneTextStep("z", "2004-10-19 10:23:54+02"))
					}
				})
			}
		})
	}
}

func TestReferenceTimezoneDateStyle(t *testing.T) {
	p := timezoneReferencePair(t)
	for _, tc := range []struct{ style, want string }{{"ISO,MDY", "2003-01-02"}, {"ISO,DMY", "2003-02-01"}, {"ISO,YMD", "2001-02-03"}} {
		timezoneBoth(t, p, referenceExec("SET DateStyle='"+tc.style+"'", 0))
		timezoneBoth(t, p, timezoneTextStep("DATE '01/02/03'", tc.want))
		raw := referenceSelect("SELECT DATE '01/02/03' AS d FROM tz_witness", []string{"d"}, []string{"DATE"}, [][]any{{tc.want}})
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, raw)
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referencePrepared(raw))
	}
	for _, tc := range []struct{ style, date, stamp, zoned string }{
		{"ISO,MDY", "1997-12-17", "1997-12-17 07:37:16", "1997-12-17 07:37:16+00"},
		{"SQL,MDY", "12/17/1997", "12/17/1997 07:37:16", "12/17/1997 07:37:16 UTC"},
		{"SQL,DMY", "17/12/1997", "17/12/1997 07:37:16", "17/12/1997 07:37:16 UTC"},
		{"German", "17.12.1997", "17.12.1997 07:37:16", "17.12.1997 07:37:16 UTC"},
		{"Postgres,MDY", "12-17-1997", "Wed Dec 17 07:37:16 1997", "Wed Dec 17 07:37:16 1997 UTC"},
		{"Postgres,DMY", "17-12-1997", "Wed 17 Dec 07:37:16 1997", "Wed 17 Dec 07:37:16 1997 UTC"},
	} {
		timezoneBoth(t, p, referenceExec("SET DateStyle='"+tc.style+"'", 0))
		timezoneBoth(t, p, timezoneTextStep("DATE '1997-12-17'", tc.date))
		timezoneBoth(t, p, timezoneTextStep("TIMESTAMP '1997-12-17 07:37:16'", tc.stamp))
		timezoneBoth(t, p, timezoneTextStep("TIMESTAMPTZ '1997-12-17 07:37:16+00'", tc.zoned))
		timezoneBoth(t, p, timezoneTextStep("TIME '07:37:16'", "07:37:16"))
		timezoneBoth(t, p, timezoneTextStep("TIMETZ '07:37:16+02'", "07:37:16+02"))
		raw := referenceSelect("SELECT DATE '1997-12-17' AS d,TIMESTAMP '1997-12-17 07:37:16'::TEXT AS c,TIMETZ '12:00+02' AS q FROM tz_witness", []string{"d", "c", "q"}, []string{"DATE", "TEXT", "TIMETZ"}, [][]any{{tc.date, tc.stamp, "12:00:00+02"}})
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, raw)
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referencePrepared(raw))
	}
	for _, tc := range []struct{ input, want string }{{"ISO,DMY", "ISO, DMY"}, {"SQL", "SQL, DMY"}, {"US", "SQL, MDY"}, {"European", "SQL, DMY"}, {"NonEuropean", "SQL, MDY"}, {"German", "German, DMY"}, {"ISO,ISO,DMY,DMY", "ISO, DMY"}, {"DEFAULT,DMY", "ISO, DMY"}} {
		timezoneBoth(t, p, referenceExec("SET DateStyle='"+tc.input+"'", 0))
		timezoneBoth(t, p, referenceSelect("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{tc.want}}))
	}
	timezoneBoth(t, p, timezoneTextStep("DATE '31/12/2024'", "2024-12-31"))
}

func TestReferenceTimezoneSettingsLifecycleAndOffsets(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := timezoneReferencePair(t)
			run := func(s referenceStep) { s.prepared = prepared; p.step(t, s) }
			defaults := make(map[*sql.DB]string)
			for _, backend := range p.backends() {
				defaults[backend.db] = timezoneDefaultZone(t, p, backend.db, backend.pg)
			}
			run(referenceExec("SET TIME ZONE 'UTC'", 0))
			for _, tc := range []struct{ operand, show, local, display string }{
				{"-7", "<-07>+07", "2004-01-01 07:00:00", "2004-01-01 05:00:00-07"},
				{"5.5", "<+05:30>-05:30", "2003-12-31 18:30:00", "2004-01-01 17:30:00+05:30"},
				{"16", "<+16>-16", "2003-12-31 08:00:00", "2004-01-02 04:00:00+16"},
				{"INTERVAL '-08:00' HOUR TO MINUTE", "<-08>+08", "2004-01-01 08:00:00", "2004-01-01 04:00:00-08"},
				{"INTERVAL '90' MINUTE", "<+01:30>-01:30", "2003-12-31 22:30:00", "2004-01-01 13:30:00+01:30"},
				{"INTERVAL '1.999999 seconds'", "<+00:00:01>-00:00:01", "2003-12-31 23:59:59", "2004-01-01 12:00:01+00:00:01"},
				{"'UTC0:00:60'", "UTC0:00:60", "2004-01-01 00:01:00", "2004-01-01 11:59:00-00:01"},
				{"'UTC-2'", "UTC-2", "2003-12-31 22:00:00", "2004-01-01 14:00:00+02"},
			} {
				run(referenceExec("SET TIME ZONE "+tc.operand, 0))
				run(referenceSelect("SHOW TIME ZONE", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{tc.show}}))
				run(timezoneTextStep("('2004-01-01 00:00'::TEXT)::TIMESTAMPTZ AT TIME ZONE '+00'", tc.local))
				run(timezoneTextStep("TIMESTAMPTZ '2004-01-01 12:00+00'", tc.display))
			}
			run(referenceExec("SET TIME ZONE 'EST5EDT'", 0))
			run(timezoneTextStep("('1974-01-10 12:00'::TEXT)::TIMESTAMPTZ AT TIME ZONE 'UTC'", "1974-01-10 16:00:00"))
			run(referenceExec("SET TIME ZONE 'ABC5DEF'", 0))
			run(timezoneTextStep("('2004-07-01 12:00'::TEXT)::TIMESTAMPTZ AT TIME ZONE 'UTC'", "2004-07-01 16:00:00"))
			for _, q := range []string{"SET TIME ZONE LOCAL", "SET TIME ZONE DEFAULT", "RESET TIME ZONE", "RESET ALL"} {
				run(referenceExec(q, 0))
				for _, backend := range p.backends() {
					s := referenceSelect("SHOW TIME ZONE", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{defaults[backend.db]}})
					s.prepared = prepared
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
				}
			}
			run(referenceExec("SET TIME ZONE 'UTC'", 0))
			run(referenceSelect("SELECT set_config('TimeZone','America/New_York',false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"America/New_York"}}))
			run(referenceSelect("SELECT current_setting('timezone')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{"America/New_York"}}))
			run(referenceExec("SET SESSION TIME ZONE 'UTC'", 0))
			run(referenceExec("BEGIN", 0))
			run(referenceExec("SET LOCAL TIME ZONE 'America/New_York'", 0))
			run(referenceExec("SET LOCAL DateStyle='SQL,DMY'", 0))
			run(timezoneTextStep("TIMESTAMPTZ '2004-07-01 16:00+00'", "01/07/2004 12:00:00 EDT"))
			run(referenceExec("COMMIT", 0))
			run(referenceSelect("SHOW TIME ZONE", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"UTC"}}))
			run(referenceSelect("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"ISO, MDY"}}))
			run(referenceExec("BEGIN", 0))
			run(referenceExec("SET TIME ZONE 'America/New_York'", 0))
			run(referenceExec("SET DateStyle='German'", 0))
			run(referenceExec("SAVEPOINT s", 0))
			run(referenceExec("SET TIME ZONE 'UTC-2'", 0))
			run(referenceExec("SET DateStyle='SQL,MDY'", 0))
			run(referenceExec("ROLLBACK TO s", 0))
			run(timezoneTextStep("TIMESTAMPTZ '2004-07-01 16:00+00'", "01.07.2004 12:00:00 EDT"))
			run(referenceExec("ROLLBACK", 0))
			run(referenceSelect("SHOW TIME ZONE", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"UTC"}}))
			run(referenceSelect("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"ISO, MDY"}}))
		})
	}
}
