package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"time"
)

// Independently specified from PostgreSQL 18 Tables 9.33/9.34; Main exercised
// the complete Phase A family against the pinned real oracle before release.
type datetimeZoneSignature struct {
	name, result, text, operator string
	args, formals                []string
}

var datetimeZoneSignatures = []datetimeZoneSignature{
	{"timezone", "TIMESTAMPTZ", "2004-07-01 16:00:00+00", "TIMESTAMP '2004-07-01 12:00' AT TIME ZONE 'America/New_York'", []string{"'America/New_York'::TEXT", "TIMESTAMP '2004-07-01 12:00'"}, []string{"TEXT", "TIMESTAMP"}},
	{"timezone", "TIMESTAMPTZ", "2004-07-01 10:00:00+00", "TIMESTAMP '2004-07-01 12:00' AT TIME ZONE INTERVAL '2 hours'", []string{"INTERVAL '2 hours'", "TIMESTAMP '2004-07-01 12:00'"}, []string{"INTERVAL", "TIMESTAMP"}},
	{"timezone", "TIMESTAMP", "2004-07-01 12:00:00", "TIMESTAMPTZ '2004-07-01 16:00+00' AT TIME ZONE 'America/New_York'", []string{"'America/New_York'::TEXT", "TIMESTAMPTZ '2004-07-01 16:00+00'"}, []string{"TEXT", "TIMESTAMPTZ"}},
	{"timezone", "TIMESTAMP", "2004-07-01 18:00:00", "TIMESTAMPTZ '2004-07-01 16:00+00' AT TIME ZONE INTERVAL '2 hours'", []string{"INTERVAL '2 hours'", "TIMESTAMPTZ '2004-07-01 16:00+00'"}, []string{"INTERVAL", "TIMESTAMPTZ"}},
	{"timezone", "TIMETZ", "10:34:17+00", "TIMETZ '05:34:17-05' AT TIME ZONE 'UTC'", []string{"'UTC'::TEXT", "TIMETZ '05:34:17-05'"}, []string{"TEXT", "TIMETZ"}},
	{"timezone", "TIMETZ", "12:34:17+02", "TIMETZ '05:34:17-05' AT TIME ZONE INTERVAL '2 hours'", []string{"INTERVAL '2 hours'", "TIMETZ '05:34:17-05'"}, []string{"INTERVAL", "TIMETZ"}},
	{"timezone", "TIMESTAMPTZ", "2004-07-01 12:00:00+00", "TIMESTAMP '2004-07-01 12:00' AT LOCAL", []string{"TIMESTAMP '2004-07-01 12:00'"}, []string{"TIMESTAMP"}},
	{"timezone", "TIMESTAMP", "2004-07-01 16:00:00", "TIMESTAMPTZ '2004-07-01 16:00+00' AT LOCAL", []string{"TIMESTAMPTZ '2004-07-01 16:00+00'"}, []string{"TIMESTAMPTZ"}},
	{"timezone", "TIMETZ", "10:34:17+00", "TIMETZ '05:34:17-05' AT LOCAL", []string{"TIMETZ '05:34:17-05'"}, []string{"TIMETZ"}},
	{"date_add", "TIMESTAMPTZ", "2024-02-29 12:00:00.123456+00", "", []string{"TIMESTAMPTZ '2024-01-31 12:00:00.123456+00'", "INTERVAL '1 month'"}, []string{"TIMESTAMPTZ", "INTERVAL"}},
	{"date_add", "TIMESTAMPTZ", "2024-02-29 12:00:00.123456+00", "", []string{"TIMESTAMPTZ '2024-01-31 12:00:00.123456+00'", "INTERVAL '1 month'", "'UTC'::TEXT"}, []string{"TIMESTAMPTZ", "INTERVAL", "TEXT"}},
	{"date_subtract", "TIMESTAMPTZ", "2023-12-31 12:00:00.123456+00", "", []string{"TIMESTAMPTZ '2024-01-31 12:00:00.123456+00'", "INTERVAL '1 month'"}, []string{"TIMESTAMPTZ", "INTERVAL"}},
	{"date_subtract", "TIMESTAMPTZ", "2023-12-31 12:00:00.123456+00", "", []string{"TIMESTAMPTZ '2024-01-31 12:00:00.123456+00'", "INTERVAL '1 month'", "'UTC'::TEXT"}, []string{"TIMESTAMPTZ", "INTERVAL", "TEXT"}},
}

func datetimeZoneScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, q := range []string{"SET TIME ZONE 'UTC'", "SET DateStyle='ISO,MDY'", "SET IntervalStyle='postgres'", "CREATE TABLE tz_witness(id INTEGER,z TIMESTAMPTZ,q TIMETZ,d DATE,c TIMESTAMP)", "INSERT INTO tz_witness(id) VALUES(1)"} {
				n := int64(0)
				if strings.HasPrefix(q, "INSERT") {
					n = 1
				}
				referenceRunStep(t, p.ctx, c, backend.recorder, referenceExec(q, n))
			}
			body(t, p, backend, c)
		})
	}
}

func datetimeZoneText(t *testing.T, p *referencePair, b referenceBackend, c referenceExecutor, expr, want string) {
	t.Helper()
	datetimeArithmeticBoth(t, p, b, c, timezoneTextStep(expr, want))
}

func datetimeZoneOverloads(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	for _, tc := range datetimeZoneSignatures {
		t.Run(tc.name+"_"+strings.Join(tc.formals, "_"), func(t *testing.T) {
			expr := tc.name + "(" + strings.Join(tc.args, ",") + ")"
			var raw any
			if tc.result == "TIMETZ" {
				raw = tc.text
				if b.pg {
					raw = time.Date(0, 1, 1, 10, 34, 17, 0, time.UTC)
				}
			} else {
				layout := "2006-01-02 15:04:05"
				if tc.result == "TIMESTAMPTZ" {
					layout += "Z07"
				}
				value, err := time.Parse(layout, tc.text)
				if err != nil {
					t.Fatal(err)
				}
				if tc.result == "TIMESTAMPTZ" {
					value = value.UTC()
				}
				raw = value
			}
			s := referenceSelect("SELECT "+expr+" AS result,("+expr+")::TEXT AS rendered FROM tz_witness WHERE id=1", []string{"result", "rendered"}, []string{tc.result, "TEXT"}, [][]any{{raw, tc.text}})
			s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
				meta, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				datetimeArithmeticMetadata(t, b.pg, meta)
			}
			datetimeArithmeticBoth(t, p, b, c, s)
			if tc.operator != "" {
				datetimeZoneText(t, p, b, c, tc.operator, tc.text)
			}
			for i, formal := range tc.formals {
				args := append([]string(nil), tc.args...)
				args[i] = "NULL::" + formal
				q := "SELECT " + tc.name + "(" + strings.Join(args, ",") + ") AS result FROM tz_witness WHERE id=1"
				datetimeArithmeticBoth(t, p, b, c, referenceSelect(q, []string{"result"}, []string{tc.result}, [][]any{{nil}}))
			}
		})
	}
}

func TestReferenceDatetimeZoneOverloadsAndTypes(t *testing.T) {
	datetimeZoneScenario(t, datetimeZoneOverloads)
}

func TestReferenceDatetimeZoneResolutionAndNulls(t *testing.T) {
	datetimeZoneScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct{ expr, typ, want string }{
			{"timezone('UTC','2004-07-01 12:00+00')", "TIMESTAMP", "2004-07-01 12:00:00"},
			{"timezone('UTC',DATE '2004-07-01')", "TIMESTAMP", "2004-07-01 00:00:00"},
			{"timezone('UTC',TIME '12:00')", "TIMETZ", "12:00:00+00"},
			{"pg_catalog.timezone('UTC',TIMESTAMP '2004-07-01 12:00')", "TIMESTAMPTZ", "2004-07-01 12:00:00+00"},
			{"\"timezone\"('UTC',TIMESTAMP '2004-07-01 12:00')", "TIMESTAMPTZ", "2004-07-01 12:00:00+00"},
			{"timezone('UTC'::VARCHAR,TIMESTAMP '2004-07-01 12:00')", "TIMESTAMPTZ", "2004-07-01 12:00:00+00"},
			{"timezone('UTC'::CHAR(3),TIMESTAMP '2004-07-01 12:00')", "TIMESTAMPTZ", "2004-07-01 12:00:00+00"},
			{"timezone('UTC',TIMESTAMP(3) '2004-07-01 12:00:00.1235')", "TIMESTAMPTZ", "2004-07-01 12:00:00.124+00"},
		} {
			t.Run(tc.expr, func(t *testing.T) {
				datetimeZoneText(t, p, b, c, tc.expr, tc.want)
				datetimeArithmeticBoth(t, p, b, c, referenceSelect("SELECT "+tc.expr+" AS result FROM tz_witness WHERE FALSE", []string{"result"}, []string{tc.typ}, nil))
			})
		}
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE DOMAIN dz_zone AS TEXT", 0))
		datetimeZoneText(t, p, b, c, "timezone('UTC'::dz_zone,TIMESTAMP '2004-07-01 12:00')", "2004-07-01 12:00:00+00")
		for _, tc := range []struct{ expr, typ string }{
			{"timezone(NULL)", "TIMESTAMP"}, {"timezone('UTC',NULL)", "TIMESTAMP"},
			{"timezone('No/Such_Zone',NULL::TIMESTAMP)", "TIMESTAMPTZ"},
			{"date_add(NULL::TIMESTAMPTZ,INTERVAL '0','No/Such_Zone')", "TIMESTAMPTZ"},
			{"date_subtract(TIMESTAMPTZ 'infinity',INTERVAL '0',NULL::TEXT)", "TIMESTAMPTZ"},
		} {
			datetimeArithmeticBoth(t, p, b, c, referenceSelect("SELECT "+tc.expr+" AS result FROM tz_witness WHERE id=1", []string{"result"}, []string{tc.typ}, [][]any{{nil}}))
		}
		datetimeArithmeticBoth(t, p, b, c, referenceSelect("SELECT timezone($1,$2::TIMESTAMP)::TEXT AS result FROM tz_witness WHERE id=1", []string{"result"}, []string{"TEXT"}, [][]any{{"2004-07-01 16:00:00+00"}}, "America/New_York", "2004-07-01 12:00"))
		datetimeZoneText(t, p, b, c, "CASE WHEN TRUE THEN TIMETZ '05:34:17-05' ELSE timezone('No/Such_Zone',TIMETZ '10:00+00') END", "05:34:17-05")
	})
}
