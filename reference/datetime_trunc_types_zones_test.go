package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceDatetimeTruncSignaturesMetadata(t *testing.T) {
	datetimeTruncScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct {
			expr, typ string
			want      any
		}{
			{"date_trunc('day',TIMESTAMP(2) '2001-02-16 20:38:40.12')", "TIMESTAMP", "2001-02-16T00:00:00Z"},
			{"date_trunc('day',TIMESTAMPTZ '2001-02-16 20:38:40+00')", "TIMESTAMPTZ", "2001-02-16T00:00:00Z"},
			{"date_trunc('minute',INTERVAL '1 day 12:34:56')", "INTERVAL", "1 day 12:34:00"},
			{"date_trunc('day',DATE '2001-02-16')", "TIMESTAMPTZ", "2001-02-16T00:00:00Z"},
			{"date_trunc('minute',TIME '12:34:56.123456')", "INTERVAL", "12:34:00"},
			{"date_trunc('hour',TIME '24:00')", "INTERVAL", "24:00:00"}, {"date_trunc('day',TIME '24:00')", "INTERVAL", "00:00:00"},
			{"date_trunc('day','2001-02-16 20:38:40+00','UTC')", "TIMESTAMPTZ", "2001-02-16T00:00:00Z"},
			{"date_trunc('bad',NULL::timestamp)", "TIMESTAMP", nil}, {"date_trunc('bad',NULL::interval)", "INTERVAL", nil},
			{"date_trunc('bad',NULL::timestamptz,'bad')", "TIMESTAMPTZ", nil},
			{"date_trunc(NULL::text,TIMESTAMPTZ '2001-01-01+00','bad')", "TIMESTAMPTZ", nil},
			{"date_trunc('bad',TIMESTAMPTZ '2001-01-01+00',NULL::text)", "TIMESTAMPTZ", nil},
		} {
			t.Run(tc.expr, func(t *testing.T) { datetimeTruncValue(t, p, b, c, tc.expr, tc.typ, tc.want) })
		}
		for _, typ := range []string{"timestamp", "timestamptz", "interval"} {
			s := referenceSelect("SELECT date_trunc('day',NULL::"+typ+") AS result WHERE false", []string{"result"}, []string{map[string]string{"timestamp": "TIMESTAMP", "timestamptz": "TIMESTAMPTZ", "interval": "INTERVAL"}[typ]}, nil)
			datetimeExtractBoth(t, p, b, c, s)
		}
		for _, domain := range []struct {
			name, base, source, typ string
			want                    any
		}{
			{"dt_stamp", "timestamp", "2001-02-16 12:34:56", "TIMESTAMP", "2001-02-16T00:00:00Z"},
			{"dt_clock", "time", "24:00", "INTERVAL", "00:00:00"},
			{"dt_date", "date", "2001-02-16", "TIMESTAMPTZ", "2001-02-16T00:00:00Z"},
		} {
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE DOMAIN "+domain.name+" AS "+domain.base, 0))
			datetimeTruncValue(t, p, b, c, "date_trunc('day','"+domain.source+"'::"+domain.name+")", domain.typ, domain.want)
		}
		for _, zone := range []struct{ name, want string }{{"UTC", "2001-02-16T00:00:00Z"}, {"America/New_York", "2001-02-16T05:00:00Z"}} {
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET TIME ZONE '"+zone.name+"'", 0))
			datetimeTruncValue(t, p, b, c, "date_trunc('day',DATE '2001-02-16') AT TIME ZONE 'UTC'", "TIMESTAMP", zone.want)
			for _, source := range []string{"TIMESTAMP '2001-02-16 20:38:40'", "DATE '2001-02-16'"} {
				q := "SELECT date_trunc('day'," + source + ",'UTC') AT TIME ZONE 'UTC' AS result"
				if b.pg {
					want := "2001-02-16T00:00:00Z"
					if zone.name == "America/New_York" && source == "TIMESTAMP '2001-02-16 20:38:40'" {
						want = "2001-02-17T00:00:00Z"
					}
					datetimeExtractBoth(t, p, b, c, referenceSelect(q, []string{"result"}, []string{"TIMESTAMP"}, [][]any{{datetimeTruncInstant(want)}}))
				} else {
					d := controlsReferenceDiagnostic("0A000", "date_trunc with time zone requires timestamp with time zone")
					d.Position = "8"
					datetimeExtractFailure(t, p, b, c, q, d, "prepare")
				}
			}
		}
	})
}

func datetimeTruncZones(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	t.Helper()
	for _, tc := range []struct{ field, source, zone, want string }{
		{"day", "2001-02-16 20:38:40+00", "Australia/Sydney", "2001-02-16T13:00:00Z"},
		{"day", "2024-03-10 12:00+00", "America/New_York", "2024-03-10T05:00:00Z"},
		{"day", "2024-11-03 12:00+00", "America/New_York", "2024-11-03T04:00:00Z"},
		{"hour", "2024-11-03 05:30+00", "America/New_York", "2024-11-03T05:00:00Z"},
		{"hour", "2024-11-03 06:30+00", "America/New_York", "2024-11-03T06:00:00Z"},
		{"day", "2018-11-04 12:00+00", "America/Sao_Paulo", "2018-11-04T03:00:00Z"},
		{"hour", "2024-04-06 14:45+00", "Australia/Lord_Howe", "2024-04-06T14:00:00Z"},
		{"hour", "2024-04-06 15:15+00", "Australia/Lord_Howe", "2024-04-06T14:30:00Z"},
		{"hour", "2024-10-05 15:45+00", "Australia/Lord_Howe", "2024-10-05T15:00:00Z"},
		{"day", "2001-02-16 20:38:40+00", "EST", "2001-02-16T05:00:00Z"},
		{"day", "2001-02-16 20:38:40+00", "GMT", "2001-02-16T00:00:00Z"},
		{"day", "2001-02-16 20:38:40+00", "gmt+3", "2001-02-16T03:00:00Z"},
		{"day", "2001-02-16 20:38:40+00", "australia/sydney", "2001-02-16T13:00:00Z"},
		{"day", "2008-01-01 12:00+00", "VET", "2008-01-01T04:30:00Z"},
		{"day", "2012-01-01 12:00+00", "MSK", "2011-12-31T20:00:00Z"},
	} {
		t.Run(tc.field+tc.source+tc.zone, func(t *testing.T) {
			datetimeTruncValue(t, p, b, c, "date_trunc($1::text,$2::timestamptz,$3::text) AT TIME ZONE 'UTC'", "TIMESTAMP", tc.want, tc.field, tc.source, tc.zone)
		})
	}
	q := "SELECT date_trunc($1::text,$2::timestamptz) AT TIME ZONE 'UTC' AS result"
	stmt, err := c.PrepareContext(p.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	x := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
	for _, tc := range []struct{ zone, want string }{{"UTC", "2024-03-10T00:00:00Z"}, {"America/New_York", "2024-03-10T05:00:00Z"}, {"UTC", "2024-03-10T00:00:00Z"}} {
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET TIME ZONE '"+tc.zone+"'", 0))
		referenceRunStep(t, p.ctx, x, b.recorder, referenceSelect(q, []string{"result"}, []string{"TIMESTAMP"}, [][]any{{datetimeTruncInstant(tc.want)}}, "day", "2024-03-10 12:00+00"))
		datetimeTruncValue(t, p, b, c, "date_trunc('day',TIMESTAMPTZ '2001-02-16 20:38:40+00','Australia/Sydney') AT TIME ZONE 'UTC'", "TIMESTAMP", "2001-02-16T13:00:00Z")
	}
}

func TestReferenceDatetimeTruncZonesDST(t *testing.T) { datetimeTruncScenario(t, datetimeTruncZones) }
