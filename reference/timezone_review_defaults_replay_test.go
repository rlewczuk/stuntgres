package reference_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"stuntgres"
	"testing"
)

const timezoneReviewDefaultsDDL = "CREATE TABLE tz_defaults(id INTEGER,z TIMESTAMPTZ DEFAULT (TIMESTAMP '2004-07-01 12:00' AT LOCAL),d TIMESTAMPTZ DEFAULT DATE '2004-10-19'::TIMESTAMPTZ,s TEXT DEFAULT DATE '2004-10-19'::TEXT,b BOOL DEFAULT (DATE '2004-10-19'=TIMESTAMPTZ '2004-10-19 04:00+00'),k BOOL DEFAULT CASE DATE '2004-10-19' WHEN TIMESTAMPTZ '2004-10-19 04:00+00' THEN TRUE ELSE FALSE END,n DATE DEFAULT NULLIF(DATE '2004-10-19',TIMESTAMPTZ '2004-10-19 04:00+00'))"
const timezoneReviewDefaultsQuery = "SELECT id,(z AT TIME ZONE 'UTC')::TEXT AS z,(d AT TIME ZONE 'UTC')::TEXT AS d,s,b,k,n::TEXT AS n FROM tz_defaults ORDER BY id"

func TestReferenceTimezoneReviewStableDefaultsReplay(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(fmt.Sprint(populated), func(t *testing.T) {
			p := timezoneReferencePair(t)
			p.step(t, referenceExec("SET TIME ZONE 'America/New_York'", 0))
			p.step(t, referenceExec(timezoneReviewDefaultsDDL, 0))
			p.step(t, referenceExec("CREATE TABLE tz_deferred(id INTEGER,z TIMESTAMPTZ DEFAULT (TIMESTAMP '2004-07-01 12:00' AT TIME ZONE 'No/Such_Zone'))", 0))
			p.step(t, referenceExec("INSERT INTO tz_deferred VALUES(1,NULL)", 1))
			if populated {
				p.step(t, referenceExec("INSERT INTO tz_defaults(id) VALUES(1)", 1))
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(dump.String(), "AT LOCAL") || !strings.Contains(dump.String(), "No/Such_Zone") {
				t.Fatal("lost deferred definition", dump.String())
			}
			native, wrapped, record := timezoneDestination(t)
			target, err := wrapped.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer target.Close()
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, target, record, s) }
			run(referenceExec("SET TIME ZONE 'Asia/Tokyo'", 0))
			run(referenceExec("SET DateStyle='SQL,DMY'", 0))
			run(referenceExec("CREATE TABLE sentinel(v INTEGER)", 0))
			run(referenceExec("INSERT INTO sentinel VALUES(7)", 1))
			held, err := target.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, record), "SELECT v FROM sentinel")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			if err := stuntgres.Restore(p.ctx, native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			referenceRunStep(t, p.ctx, timezoneAdmittedRows{referenceExecutor: target, rows: held}, record, referenceSelect("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(referenceSelect("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"Asia/Tokyo"}}))
			run(referenceSelect("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"SQL, DMY"}}))
			run(referenceExec("INSERT INTO tz_defaults(id) VALUES(2)", 1))
			run(referenceExec("SET DateStyle='ISO,MDY'", 0))
			want := [][]any{}
			if populated {
				want = append(want, []any{int64(1), "2004-07-01 16:00:00", "2004-10-19 04:00:00", "2004-10-19", true, true, nil})
			}
			want = append(want, []any{int64(2), "2004-07-01 03:00:00", "2004-10-18 15:00:00", "19/10/2004", false, false, "2004-10-19"})
			output := referenceSelect(timezoneReviewDefaultsQuery, []string{"id", "z", "d", "s", "b", "k", "n"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "TEXT"}, want)
			run(output)
			failure := referenceImplicitError("INSERT INTO tz_deferred(id) VALUES(2)", referenceDiagnostic{Code: "22023", Message: `time zone "No/Such_Zone" not recognized`})
			failure.errorStage = "execute"
			run(failure)
			sentinel := referenceSelect("SELECT id,z::TEXT AS z FROM tz_deferred ORDER BY id", []string{"id", "z"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}})
			run(sentinel)
			// Independent consumers on every selected backend exercise the same live
			// default environment without pretending PostgreSQL offers native Restore.
			p.step(t, referenceExec("SET TIME ZONE 'Asia/Tokyo'", 0))
			p.step(t, referenceExec("SET DateStyle='SQL,DMY'", 0))
			p.step(t, referenceExec("INSERT INTO tz_defaults(id) VALUES(2)", 1))
			p.step(t, referenceExec("SET DateStyle='ISO,MDY'", 0))
			timezoneBoth(t, p, output)
			timezoneBoth(t, p, failure)
			timezoneBoth(t, p, sentinel)
		})
	}
}
