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

// PostgreSQL applies months (clamping), days, then elapsed microseconds.
func uuidGenerationCalendar(t time.Time, months, days int, elapsed time.Duration) time.Time {
	y, m, d := t.Date()
	first := time.Date(y, m+time.Month(months), 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
	last := time.Date(first.Year(), first.Month()+1, 0, 0, 0, 0, 0, t.Location()).Day()
	if d > last {
		d = last
	}
	return time.Date(first.Year(), first.Month(), d, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location()).AddDate(0, 0, days).Add(elapsed)
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
	before := time.Now()
	v := uuidGenerationRead(t, p, b, fixture.HeldStatement{Executor: c, Statement: literal, SQL: "SELECT uuidv7('1 02:03:04')"}, "SELECT uuidv7('1 02:03:04')")
	_, ms := uuidGenerationDecode(t, *v, 7)
	uuidGenerationWindow(t, ms, before.Add(26*time.Hour+3*time.Minute+4*time.Second), time.Now().Add(26*time.Hour+3*time.Minute+4*time.Second))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET IntervalStyle='postgres'", 0))
}

func uuidGenerationGuards(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	// The independent wrap bracket below uses UTC calendar dates. Do not inherit
	// a prior scenario's session zone (month arithmetic re-resolves its offset).
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TimeZone='UTC'", 0))
	for _, tc := range []struct {
		shift                 string
		months                int
		code, message, detail string
	}{
		{"infinity", 0, "22008", "interval out of range for UUID version 7", "UUID version 7 does not support infinite intervals."},
		{"-infinity", 0, "22008", "interval out of range for UUID version 7", "UUID version 7 does not support infinite intervals."},
		{"-1000 years", -12000, "22008", "timestamp out of range for UUID version 7", "UUID version 7 supports timestamps from 1970-01-01 to approximately year 10889."},
		{"9000 years", 108000, "22008", "timestamp out of range for UUID version 7", "UUID version 7 supports timestamps from 1970-01-01 to approximately year 10889."},
		{"2147483647 months", 0, "22008", "timestamp out of range", ""},
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
				before := time.Now().UTC()
				var text string
				if parameter {
					err = stmt.QueryRowContext(p.Ctx, args...).Scan(&text)
				} else {
					err = c.QueryRowContext(p.Ctx, query).Scan(&text)
				}
				after := time.Now().UTC()
				stmt.Close()
				if !b.PG || tc.shift == "2147483647 months" {
					uuidGenerationError(t, err, uuidGenerationDiagnostic(tc.code, tc.message, tc.detail))
					continue
				}
				if err != nil {
					t.Fatalf("pinned18.0 unchecked shift %q: %v", tc.shift, err)
				}
				_, ms := uuidGenerationDecode(t, text, 7)
				switch tc.shift {
				case "infinity", "-infinity":
					// Pinned18.0 succeeds with valid version/variant (observed by
					// the root). Its unchecked signed sentinel multiplication
					// overflows C int64, so there is no portable timestamp golden.
					// Native18.6 instead asserts the exact finite-shift diagnostic.
				default:
					const modulus = int64(1) << 48
					low := uuidGenerationCalendar(before, tc.months, 0, 0).UnixMilli()
					high := uuidGenerationCalendar(after, tc.months, 0, 0).UnixMilli()
					low = (low%modulus + modulus) % modulus
					high = (high%modulus + modulus) % modulus
					if ms < low-2000 || ms > high+2000 {
						t.Fatalf("wrapped UUID ms %d outside [%d,%d]", ms, low-2000, high+2000)
					}
				}
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
