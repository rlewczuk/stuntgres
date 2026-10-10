package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"
)

func datetimeOverlapsDiagnostic(code, message, hint, query, anchor string) referenceDiagnostic {
	d := controlsReferenceDiagnostic(code, message)
	d.Hint = hint
	if anchor != "" {
		d.Position = referencePosition(utf8.RuneCountInString(query[:strings.Index(query, anchor)]) + 1)
	}
	return d
}

func TestReferenceDatetimeOverlapsDiagnostics(t *testing.T) {
	datetimeOverlapsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		missing := "No function matches the given name and argument types. You might need to add explicit type casts."
		ambiguous := "Could not choose a best candidate function. You might need to add explicit type casts."
		for _, tc := range []struct{ query, signature, anchor string }{
			{"SELECT (1,2) OVERLAPS (3,4)", "pg_catalog.overlaps(integer, integer, integer, integer)", "OVERLAPS"},
			{"/*é*/ SELECT (true,false) OVERLAPS (true,false)", "pg_catalog.overlaps(boolean, boolean, boolean, boolean)", "OVERLAPS"},
			{"SELECT ('a'::text,'b'::text) OVERLAPS ('c'::text,'d'::text)", "pg_catalog.overlaps(text, text, text, text)", "OVERLAPS"},
			{"SELECT (TIME '01:00',TIME '02:00') OVERLAPS (TIMESTAMP '2001-01-01',TIMESTAMP '2001-01-02')", "pg_catalog.overlaps(time without time zone, time without time zone, timestamp without time zone, timestamp without time zone)", "OVERLAPS"},
			{"SELECT (INTERVAL '1 hour',TIME '02:00') OVERLAPS (TIME '01:00',TIME '03:00')", "pg_catalog.overlaps(interval, time without time zone, time without time zone, time without time zone)", "OVERLAPS"},
			{"SELECT (TIMETZ '01:00+00',INTERVAL '1 hour') OVERLAPS (TIMETZ '01:00+00',TIMETZ '03:00+00')", "pg_catalog.overlaps(time with time zone, interval, time with time zone, time with time zone)", "OVERLAPS"},
			{"SELECT overlaps()", "overlaps()", "overlaps"},
			{"SELECT overlaps(TIME '01:00')", "overlaps(time without time zone)", "overlaps"},
			{"SELECT overlaps(1,2,3,4,5)", "overlaps(integer, integer, integer, integer, integer)", "overlaps"},
			{"SELECT pg_catalog.overlaps(1,2,3,4)", "pg_catalog.overlaps(integer, integer, integer, integer)", "pg_catalog"},
			{"SELECT \"OVERLAPS\"(TIME '01:00',TIME '03:00',TIME '02:00',TIME '04:00')", "OVERLAPS(time without time zone, time without time zone, time without time zone, time without time zone)", "\"OVERLAPS\""},
			{"SELECT overlaps(TIME '01:00',TIME '03:00',TIME '02:00',no_such_argument => TIME '04:00')", "overlaps(time without time zone, time without time zone, time without time zone, no_such_argument => time without time zone)", "overlaps"},
		} {
			t.Run(tc.query, func(t *testing.T) {
				datetimeOverlapsFailure(t, p, b, c, tc.query, datetimeOverlapsDiagnostic("42883", "function "+tc.signature+" does not exist", missing, tc.query, tc.anchor))
			})
		}
		for _, tc := range []struct {
			query, signature, anchor string
			args                     []any
		}{
			{"SELECT (NULL,NULL) OVERLAPS (NULL,NULL)", "pg_catalog.overlaps(unknown, unknown, unknown, unknown)", "OVERLAPS", nil},
			{"SELECT ('2001-01-01','2001-01-03') OVERLAPS ('2001-01-02','2001-01-04')", "pg_catalog.overlaps(unknown, unknown, unknown, unknown)", "OVERLAPS", nil},
			{"SELECT ($1,$2) OVERLAPS ($3,$4)", "pg_catalog.overlaps(unknown, unknown, unknown, unknown)", "OVERLAPS", []any{nil, nil, nil, nil}},
			{"SELECT (TIME '01:00','03:00') OVERLAPS (TIME '02:00','04:00')", "pg_catalog.overlaps(time without time zone, unknown, time without time zone, unknown)", "OVERLAPS", nil},
			{"SELECT overlaps('2001-01-01','2001-01-03','2001-01-02','2001-01-04')", "overlaps(unknown, unknown, unknown, unknown)", "overlaps", nil},
			{"SELECT overlaps($1,$2,$3,$4)", "overlaps(unknown, unknown, unknown, unknown)", "overlaps", []any{nil, nil, nil, nil}},
			{"SELECT overlaps(NULL,NULL,NULL,NULL)", "overlaps(unknown, unknown, unknown, unknown)", "overlaps", nil},
		} {
			t.Run(tc.query, func(t *testing.T) {
				datetimeOverlapsFailure(t, p, b, c, tc.query, datetimeOverlapsDiagnostic("42725", "function "+tc.signature+" is not unique", ambiguous, tc.query, tc.anchor), tc.args...)
			})
		}
		for _, tc := range []struct{ query, side, anchor string }{
			{"SELECT ROW() OVERLAPS (TIME '01:00',TIME '02:00')", "left", "ROW"},
			{"SELECT ROW(TIME '01:00') OVERLAPS (TIME '01:00',TIME '02:00')", "left", "ROW"},
			{"SELECT (1,2,3) OVERLAPS (4,5)", "left", "("},
			{"SELECT (TIME '01:00',TIME '02:00') OVERLAPS ROW()", "right", "ROW"},
			{"SELECT (TIME '01:00',TIME '02:00') OVERLAPS ROW(TIME '01:00')", "right", "ROW"},
			{"SELECT (1,2) OVERLAPS (3,4,5)", "right", "(3"},
		} {
			t.Run(tc.query, func(t *testing.T) {
				datetimeOverlapsFailure(t, p, b, c, tc.query, datetimeOverlapsDiagnostic("42601", "wrong number of parameters on "+tc.side+" side of OVERLAPS expression", "", tc.query, tc.anchor))
			})
		}
		for _, tc := range []struct{ query, message, anchor string }{
			{"SELECT (1) OVERLAPS (2,3)", "syntax error at or near \"OVERLAPS\"", "OVERLAPS"},
			{"SELECT (1,2) OVERLAPS (3)", "syntax error at or near \")\"", ""},
			{"SELECT overlaps(VARIADIC '{1,2,3,4}'::integer[])", "function overlaps(integer[]) does not exist", "overlaps"},
		} {
			t.Run(tc.query, func(t *testing.T) {
				code, hint := "42601", ""
				if strings.Contains(tc.query, "VARIADIC") {
					code, hint = "42883", missing
				}
				d := datetimeOverlapsDiagnostic(code, tc.message, hint, tc.query, tc.anchor)
				if tc.query == "SELECT (1,2) OVERLAPS (3)" {
					d.Position = "25"
				}
				datetimeOverlapsFailure(t, p, b, c, tc.query, d)
			})
		}
		for _, tc := range []struct{ query, code, message, anchor string }{
			{"SELECT (TIMESTAMP 'bad',TIMESTAMP '2001-01-03') OVERLAPS (TIMESTAMP '2001-01-02',TIMESTAMP '2001-01-04')", "22007", "invalid input syntax for type timestamp: \"bad\"", "'bad'"},
			{"SELECT (TIME 'bad',TIME '03:00') OVERLAPS (TIME '02:00',TIME '04:00')", "22007", "invalid input syntax for type time: \"bad\"", "'bad'"},
			{"SELECT (DATE 'bad',DATE '2001-01-03') OVERLAPS (DATE '2001-01-02',DATE '2001-01-04')", "22007", "invalid input syntax for type date: \"bad\"", "'bad'"},
			{"SELECT (TIMESTAMP '2001-01-01',INTERVAL 'bad') OVERLAPS (TIMESTAMP '2001-01-02',TIMESTAMP '2001-01-04')", "22007", "invalid input syntax for type interval: \"bad\"", "'bad'"},
			{"SELECT (TIMESTAMP '294276-12-31',INTERVAL '1 day') OVERLAPS (TIMESTAMP '2001-01-02',TIMESTAMP '2001-01-04')", "22008", "timestamp out of range", ""},
			{"SELECT (TIMESTAMP 'infinity',INTERVAL '-infinity') OVERLAPS (TIMESTAMP '2001-01-02',TIMESTAMP '2001-01-04')", "22008", "timestamp out of range", ""},
		} {
			t.Run(tc.query, func(t *testing.T) {
				d := datetimeOverlapsDiagnostic(tc.code, tc.message, "", tc.query, tc.anchor)
				if tc.code == "22008" {
					d.Context = "SQL function \"overlaps\" statement 1"
				}
				datetimeOverlapsFailure(t, p, b, c, tc.query, d)
			})
		}
		q := "SELECT ($1,$1::interval) OVERLAPS (TIMESTAMP '2001-01-02',INTERVAL '1 day')"
		d := datetimeOverlapsDiagnostic("42P08", "inconsistent types deduced for parameter $1", "", q, "$1")
		d.Detail = "interval versus timestamp without time zone"
		datetimeOverlapsFailure(t, p, b, c, q, d, nil)
	})
}
