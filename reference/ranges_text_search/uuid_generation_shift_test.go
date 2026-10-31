package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
	"unicode/utf8"
)

// PostgreSQL resolves each nonzero calendar stage, then adds elapsed time.
// Civil arithmetic uses UTC solely to normalize fields without Go choosing an
// ambiguous local offset; the destination is resolved in the original zone.
func uuidGenerationCalendar(t time.Time, months, days int, elapsed time.Duration) time.Time {
	if months != 0 {
		y, m, d := t.Date()
		first := time.Date(y, m+time.Month(months), 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
		last := first.AddDate(0, 1, -1).Day()
		if d > last {
			d = last
		}
		wall := time.Date(first.Year(), first.Month(), d, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
		t = uuidGenerationResolveCivil(wall, t.Location())
	}
	if days != 0 {
		y, m, d := t.Date()
		wall := time.Date(y, m, d+days, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
		t = uuidGenerationResolveCivil(wall, t.Location())
	}
	return t.Add(elapsed)
}

// Independent Appendix B.2 oracle for the UTC/Denver calendar fixtures.
// Enumerate nearby offset periods via public Go APIs, not time.Date's
// unspecified fold/gap choice or production timezone/arithmetic helpers.
func uuidGenerationResolveCivil(wall time.Time, loc *time.Location) time.Time {
	var valid, gap time.Time
	end := wall.Add(48 * time.Hour)
	for probe := wall.Add(-48 * time.Hour); !probe.After(end); {
		_, offset := probe.In(loc).Zone()
		candidate := wall.Add(-time.Duration(offset) * time.Second)
		local := candidate.In(loc)
		y, m, d := local.Date()
		actual := time.Date(y, m, d, local.Hour(), local.Minute(), local.Second(), local.Nanosecond(), time.UTC)
		// A fold has two valid instants: choose the later (post-transition).
		if actual.Equal(wall) && (valid.IsZero() || candidate.After(valid)) {
			valid = candidate
		}
		// In a gap neither candidate round-trips. The later instant uses the
		// pre-transition offset and normalizes forward across the gap.
		if gap.IsZero() || candidate.After(gap) {
			gap = candidate
		}
		_, next := probe.In(loc).ZoneBounds()
		if next.IsZero() || next.After(end) {
			break
		}
		probe = next
	}
	if !valid.IsZero() {
		return valid.In(loc)
	}
	return gap.In(loc)
}

func uuidGenerationShifts(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	stmt, err := c.PrepareContext(p.Ctx, "SELECT uuidv7($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: "SELECT uuidv7($1)"}
	for _, zone := range []string{"UTC", "America/Denver"} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TimeZone='"+zone+"'", 0))
		loc, err := time.LoadLocation(zone)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			arg          any
			months, days int
			elapsed      time.Duration
		}{{"0", 0, 0, 0}, {"1 day", 0, 1, 0}, {[]byte("-1 day"), 0, -1, 0}, {"1000 years", 12000, 0, 0}, {"1 month", 1, 0, 0}, {"1 month -1 day 2 hours", 1, -1, 2 * time.Hour}, {"24 hours", 0, 0, 24 * time.Hour}} {
			before := time.Now().In(loc)
			v := uuidGenerationRead(t, p, b, held, "SELECT uuidv7($1)", tc.arg)
			after := time.Now().In(loc)
			_, ms := uuidGenerationDecode(t, *v, 7)
			uuidGenerationWindow(t, ms, uuidGenerationCalendar(before, tc.months, tc.days, tc.elapsed), uuidGenerationCalendar(after, tc.months, tc.days, tc.elapsed))
		}
	}
	for _, q := range []string{"SELECT uuidv7(shift=>'0')", "SELECT uuidv7(shift:='0')", "SELECT uuidv7('0')"} {
		before := time.Now()
		v := uuidGenerationRead(t, p, b, c, q)
		_, ms := uuidGenerationDecode(t, *v, 7)
		uuidGenerationWindow(t, ms, before, time.Now())
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET IntervalStyle='postgres'", 0))
	literal, err := c.PrepareContext(p.Ctx, "SELECT uuidv7('1 02:03:04')")
	if err != nil {
		t.Fatal(err)
	}
	defer literal.Close()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET IntervalStyle='sql_standard'", 0))
	loc, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now().In(loc)
	v := uuidGenerationRead(t, p, b, fixture.HeldStatement{Executor: c, Statement: literal, SQL: "SELECT uuidv7('1 02:03:04')"}, "SELECT uuidv7('1 02:03:04')")
	_, ms := uuidGenerationDecode(t, *v, 7)
	// The postgres-style literal froze one calendar day plus 02:03:04 at
	// Prepare, not elapsed 26:03:04. Execute still uses the Denver zone.
	elapsed := 2*time.Hour + 3*time.Minute + 4*time.Second
	uuidGenerationWindow(t, ms, uuidGenerationCalendar(before, 0, 1, elapsed), uuidGenerationCalendar(time.Now().In(loc), 0, 1, elapsed))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET IntervalStyle='postgres'", 0))
}

func uuidGenerationGuards(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	// Do not inherit a prior scenario's session zone.
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TimeZone='UTC'", 0))
	for _, tc := range []struct {
		shift                 string
		code, message, detail string
	}{
		{"infinity", "22008", "interval out of range for UUID version 7", "UUID version 7 does not support infinite intervals."},
		{"-infinity", "22008", "interval out of range for UUID version 7", "UUID version 7 does not support infinite intervals."},
		{"-1000 years", "22008", "timestamp out of range for UUID version 7", "UUID version 7 supports timestamps from 1970-01-01 to approximately year 10889."},
		{"9000 years", "22008", "timestamp out of range for UUID version 7", "UUID version 7 supports timestamps from 1970-01-01 to approximately year 10889."},
		{"2147483647 months", "22008", "timestamp out of range", ""},
	} {
		t.Run(tc.shift, func(t *testing.T) {
			for _, parameter := range []bool{false, true} {
				query := "SELECT uuidv7(interval '" + tc.shift + "')"
				var args []any
				if parameter {
					query = "SELECT uuidv7($1)"
					args = []any{tc.shift}
				}
				stmt, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatalf("valid shift failed Prepare: %v", err)
				}
				var text string
				if parameter {
					err = stmt.QueryRowContext(p.Ctx, args...).Scan(&text)
				} else {
					err = c.QueryRowContext(p.Ctx, query).Scan(&text)
				}
				stmt.Close()
				// PostgreSQL 18.6 and native apply the same version-7 range guards.
				uuidGenerationError(t, err, uuidGenerationDiagnostic(tc.code, tc.message, tc.detail))
			}
		})
	}
	for _, prefix := range []string{"", "/*é*/ "} {
		query := prefix + "SELECT uuidv7('bad')"
		_, err := c.PrepareContext(p.Ctx, query)
		want := uuidGenerationDiagnostic("22007", `invalid input syntax for type interval: "bad"`, "")
		want.Position = strconv.Itoa(utf8.RuneCountInString(query[:strings.Index(query, "'bad'")]) + 1)
		uuidGenerationError(t, err, want)
	}
	stmt, err := c.PrepareContext(p.Ctx, "SELECT uuidv7($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	var raw any
	err = stmt.QueryRowContext(p.Ctx, "bad").Scan(&raw)
	want := uuidGenerationDiagnostic("22007", `invalid input syntax for type interval: "bad"`, "")
	want.Context = "unnamed portal parameter $1 = '...'"
	uuidGenerationError(t, err, want)
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: "SELECT uuidv7($1)"}
	if uuidGenerationRead(t, p, b, held, "SELECT uuidv7($1)", nil) != nil {
		t.Fatal("NULL did not bypass shift")
	}
	v := uuidGenerationRead(t, p, b, held, "SELECT uuidv7($1)", "0")
	uuidGenerationDecode(t, *v, 7)
}

func TestReferenceUUIDGenerationCalendarAndPreparedShift(t *testing.T) {
	uuidGenerationScenario(t, uuidGenerationShifts)
}
func TestReferenceUUIDGenerationGuardDiagnostics(t *testing.T) {
	uuidGenerationScenario(t, uuidGenerationGuards)
}

// Literal release/manual-derived instants, never learned from either backend or
// from the calendar helper. Keep the helper and SQL witnesses on the same cases.
var uuidGenerationCalendarCases = [...]struct {
	name, zone, source, shift string
	months, days              int
	elapsed                   time.Duration
	want                      string
}{
	{"day_into_fold", "America/Denver", "2026-10-31T01:12:42.125-06:00", "1 day", 0, 1, 0, "2026-11-01T08:12:42.125Z"},
	{"elapsed_into_first_fold", "America/Denver", "2026-10-31T01:12:42.125-06:00", "24 hours", 0, 0, 24 * time.Hour, "2026-11-01T07:12:42.125Z"},
	{"month_into_fold", "America/Denver", "2026-10-01T01:12:42.125-06:00", "1 month", 1, 0, 0, "2026-11-01T08:12:42.125Z"},
	{"day_into_gap", "America/Denver", "2026-03-07T02:30:00-07:00", "1 day", 0, 1, 0, "2026-03-08T09:30:00Z"},
	{"zero_first_fold", "America/Denver", "2026-11-01T01:12:42.125-06:00", "0", 0, 0, 0, "2026-11-01T07:12:42.125Z"},
	{"zero_second_fold", "America/Denver", "2026-11-01T01:12:42.125-07:00", "0", 0, 0, 0, "2026-11-01T08:12:42.125Z"},
	{"elapsed_first_fold", "America/Denver", "2026-11-01T01:12:42.125-06:00", "30 minutes", 0, 0, 30 * time.Minute, "2026-11-01T07:42:42.125Z"},
	{"elapsed_second_fold", "America/Denver", "2026-11-01T01:12:42.125-07:00", "30 minutes", 0, 0, 30 * time.Minute, "2026-11-01T08:42:42.125Z"},
	{"leap_month_clamp", "UTC", "2024-01-31T12:00:00Z", "1 month", 1, 0, 0, "2024-02-29T12:00:00Z"},
	{"clamp_day_elapsed", "UTC", "2023-01-31T12:00:00Z", "1 month 1 day 2 hours", 1, 1, 2 * time.Hour, "2023-03-01T14:00:00Z"},
	{"fold_month_day_elapsed", "America/Denver", "2026-10-01T01:12:42.125-06:00", "1 month -1 day 2 hours", 1, -1, 2 * time.Hour, "2026-10-31T09:12:42.125Z"},
	{"gap_month_day_elapsed", "America/Denver", "2026-02-08T02:30:00-07:00", "1 month 1 day 2 hours", 1, 1, 2 * time.Hour, "2026-03-09T11:30:00Z"},
	{"literal_day_elapsed_across_fold", "America/Denver", "2026-10-31T04:26:52.125-06:00", "1 02:03:04", 0, 1, 2*time.Hour + 3*time.Minute + 4*time.Second, "2026-11-01T13:29:56.125Z"},
	{"literal_day_elapsed_into_fold", "America/Denver", "2026-10-31T01:12:42.125-06:00", "1 02:03:04", 0, 1, 2*time.Hour + 3*time.Minute + 4*time.Second, "2026-11-01T10:15:46.125Z"},
	{"literal_day_elapsed_into_gap", "America/Denver", "2026-03-07T02:30:00-07:00", "1 02:03:04", 0, 1, 2*time.Hour + 3*time.Minute + 4*time.Second, "2026-03-08T11:33:04Z"},
}

func TestReferenceUUIDGenerationCalendarHelperBoundaries(t *testing.T) {
	for _, tc := range uuidGenerationCalendarCases {
		t.Run(tc.name, func(t *testing.T) {
			loc, err := time.LoadLocation(tc.zone)
			if err != nil {
				t.Fatal(err)
			}
			source, err := time.Parse(time.RFC3339Nano, tc.source)
			if err != nil {
				t.Fatal(err)
			}
			want, err := time.Parse(time.RFC3339Nano, tc.want)
			if err != nil {
				t.Fatal(err)
			}
			got := uuidGenerationCalendar(source.In(loc), tc.months, tc.days, tc.elapsed)
			if !got.Equal(want) {
				t.Fatalf("%s + %s in %s: got %s, want %s", tc.source, tc.shift, tc.zone, got.UTC().Format(time.RFC3339Nano), tc.want)
			}
		})
	}
}

func TestReferenceUUIDGenerationCalendarSQLBoundaries(t *testing.T) {
	uuidGenerationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET IntervalStyle='postgres'", 0))
		for _, tc := range uuidGenerationCalendarCases {
			t.Run(tc.name, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TimeZone='"+tc.zone+"'", 0))
				// Exact instant equality avoids adapter-specific timestamp
				// presentation and never compares one backend to the other.
				query := "SELECT (timestamptz '" + tc.source + "' + interval '" + tc.shift + "') = timestamptz '" + tc.want + "' AS matches"
				step := fixture.Select(query, []string{"matches"}, []string{"BOOL"}, [][]any{{true}})
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
			})
		}
	})
}
