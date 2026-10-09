package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceTimezoneReviewStableDefaults(t *testing.T) {
	for _, name := range []string{"literal", "direct", "prepared"} {
		prepared := name == "prepared"
		t.Run(name, func(t *testing.T) {
			p := timezoneReferencePair(t)
			ddl := referenceExec(timezoneReviewDefaultsDDL, 0)
			ddl.prepared = prepared
			p.step(t, ddl)
			for _, backend := range p.backends() {
				query := "INSERT INTO tz_defaults(id) VALUES($1)"
				var x referenceExecutor = backend.db
				held := timezoneHeld(t, p, backend.db, query)
				if prepared {
					x = held
				}
				for i, settings := range []struct{ zone, style string }{{"UTC", "ISO,MDY"}, {"America/New_York", "SQL,DMY"}} {
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("SET TIME ZONE '"+settings.zone+"'", 0))
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("SET DateStyle='"+settings.style+"'", 0))
					var insert referenceStep
					if name == "literal" {
						insert = referenceExec(fmt.Sprintf("INSERT INTO tz_defaults(id) VALUES(%d)", i+1), 1)
					} else {
						insert = referenceExec(query, 1, i+1)
					}
					referenceRunStep(t, p.ctx, x, backend.recorder, insert)
				}
			}
			cell := referenceExec("INSERT INTO tz_defaults VALUES(3,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT),(4,NULL,NULL,'explicit',NULL,NULL,NULL)", 2)
			cell.prepared = prepared
			p.step(t, cell)
			p.step(t, referenceExec("SET DateStyle='ISO,MDY'", 0))
			p.step(t, referenceSelect("SELECT id,(z AT TIME ZONE 'UTC')::TEXT AS z,(d AT TIME ZONE 'UTC')::TEXT AS d,s,b,k,n::TEXT AS n FROM tz_defaults ORDER BY id", []string{"id", "z", "d", "s", "b", "k", "n"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "TEXT"}, [][]any{
				{int64(1), "2004-07-01 12:00:00", "2004-10-19 00:00:00", "2004-10-19", false, false, "2004-10-19"},
				{int64(2), "2004-07-01 16:00:00", "2004-10-19 04:00:00", "19/10/2004", true, true, nil},
				{int64(3), "2004-07-01 16:00:00", "2004-10-19 04:00:00", "19/10/2004", true, true, nil},
				{int64(4), nil, nil, "explicit", nil, nil, nil},
			}))
			deferred := referenceExec("CREATE TABLE tz_deferred(id INTEGER,z TIMESTAMPTZ DEFAULT (TIMESTAMP '2004-07-01 12:00' AT TIME ZONE 'No/Such_Zone'))", 0)
			deferred.prepared = prepared
			p.step(t, deferred)
			p.step(t, referenceExec("INSERT INTO tz_deferred VALUES(1,NULL)", 1))
			failure := referenceImplicitError("INSERT INTO tz_deferred(id) VALUES(2)", referenceDiagnostic{Code: "22023", Message: `time zone "No/Such_Zone" not recognized`})
			failure.prepared = prepared
			failure.errorStage = "execute"
			p.step(t, failure)
			p.step(t, referenceSelect("SELECT id,z::TEXT AS z FROM tz_deferred ORDER BY id", []string{"id", "z"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}}))
		})
	}
}
