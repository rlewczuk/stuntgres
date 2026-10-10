package reference_test

import (
	"testing"
)

// Constants below come from the installed stock timezone assets and PostgreSQL
// 18 view declarations, not from native output or backend parity.
func timezoneViewsScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, referenceExecutor)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			c, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, query := range []string{"SET TIME ZONE 'UTC'", "SET timezone_abbreviations='Default'", "SET IntervalStyle='postgres'"} {
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(query, 0))
			}
			body(t, p, b, c)
		})
	}
}

func timezoneViewsNames(query string, rows [][]any, args ...any) referenceStep {
	return referenceSelect(query, []string{"name", "abbrev", "utc_offset", "is_dst"}, []string{"TEXT", "TEXT", "INTERVAL", "BOOL"}, rows, args...)
}

func timezoneViewsAbbrevs(query string, rows [][]any, args ...any) referenceStep {
	return referenceSelect(query, []string{"abbrev", "utc_offset", "is_dst"}, []string{"TEXT", "INTERVAL", "BOOL"}, rows, args...)
}

func TestReferenceTimezoneViewsNames(t *testing.T) {
	timezoneViewsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c referenceExecutor) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		for _, source := range []string{"pg_timezone_names", "pg_catalog.pg_timezone_names"} {
			for _, prepared := range []bool{false, true} {
				s := timezoneViewsNames("SELECT * FROM "+source+" WHERE name=$1::text", [][]any{{"UTC", "UTC", "00:00:00", false}}, "UTC")
				s.prepared = prepared
				run(s)
			}
		}
		run(timezoneViewsNames("SELECT * FROM pg_timezone_names WHERE name='Asia/Calcutta' OR name='Asia/Kolkata' OR name='Etc/GMT+5' OR name='Etc/UTC' ORDER BY name", [][]any{
			{"Asia/Calcutta", "IST", "05:30:00", false},
			{"Asia/Kolkata", "IST", "05:30:00", false},
			{"Etc/GMT+5", "-05", "-05:00:00", false},
			{"Etc/UTC", "UTC", "00:00:00", false},
		}))
		for _, name := range []string{"No/Such_Zone", "utc"} {
			run(timezoneViewsNames("SELECT * FROM pg_timezone_names WHERE name=$1::text", [][]any{}, name))
		}
		run(referenceExec("SET TIME ZONE 'ABC3DEF,M3.2.0,M11.1.0'", 0))
		run(timezoneViewsNames("SELECT * FROM pg_timezone_names WHERE name='ABC3DEF,M3.2.0,M11.1.0'", [][]any{}))
		run(referenceSelect("SELECT a.abbrev=b.abbrev AS same_abbrev,a.utc_offset=b.utc_offset AS same_offset,a.is_dst=b.is_dst AS same_dst FROM pg_timezone_names a JOIN pg_timezone_names b ON b.name='US/Eastern' WHERE a.name='America/New_York'", []string{"same_abbrev", "same_offset", "same_dst"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
		run(referenceExec("CREATE VIEW timezone_lookup_utc AS SELECT * FROM pg_catalog.pg_timezone_names WHERE name='UTC'", 0))
		run(timezoneViewsNames("SELECT * FROM timezone_lookup_utc", [][]any{{"UTC", "UTC", "00:00:00", false}}))
	})
}

func TestReferenceTimezoneViewsAbbreviations(t *testing.T) {
	timezoneViewsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c referenceExecutor) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		for _, prepared := range []bool{false, true} {
			s := timezoneViewsAbbrevs("SELECT * FROM pg_catalog.pg_timezone_abbrevs WHERE abbrev='EDT' OR abbrev='EST' OR abbrev='IST' OR abbrev='UTC' ORDER BY abbrev", [][]any{{"EDT", "-04:00:00", true}, {"EST", "-05:00:00", false}, {"IST", "02:00:00", false}, {"UTC", "00:00:00", false}})
			s.prepared = prepared
			run(s)
		}
		query := "SELECT * FROM pg_timezone_abbrevs WHERE abbrev=$1::text"
		held := timezoneHeld(t, p, c, query)
		for _, tc := range []struct{ set, zone, label, offset string }{
			{"India", "UTC", "IST", "05:30:00"},
			{"Australia", "UTC", "EST", "10:00:00"},
			{"Australia", "UTC", "CST", "09:30:00"},
			{"Australia", "UTC", "SAT", "09:30:00"},
			{"Australia", "America/New_York", "EST", "-05:00:00"},
			{"Default", "Asia/Kolkata", "IST", "05:30:00"},
			{"Default", "America/New_York", "LMT", "-04:56:02"},
			{"Default", "America/Los_Angeles", "LMT", "-07:52:58"},
		} {
			run(referenceExec("SET timezone_abbreviations='"+tc.set+"'", 0))
			run(referenceExec("SET TIME ZONE '"+tc.zone+"'", 0))
			referenceRunStep(t, p.ctx, held, b.recorder, timezoneViewsAbbrevs(query, [][]any{{tc.label, tc.offset, false}}, tc.label))
		}
		run(referenceExec("SET TIME ZONE 'Etc/GMT+5'", 0))
		run(timezoneViewsAbbrevs("SELECT * FROM pg_timezone_abbrevs WHERE abbrev='-05' OR abbrev='NoSuchAbbreviation'", [][]any{}))
	})
}
