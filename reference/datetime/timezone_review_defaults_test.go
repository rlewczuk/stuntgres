package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTimezoneReviewStableDefaults(t *testing.T) {
	for _, name := range []string{"literal", "direct", "prepared"} {
		prepared := name == "prepared"
		t.Run(name, func(t *testing.T) {
			p := fixture.TimezoneReferencePair(t)
			ddl := fixture.Exec(timezoneReviewDefaultsDDL, 0)
			ddl.Prepared = prepared
			p.Step(t, ddl)
			for _, backend := range p.Backends() {
				query := "INSERT INTO tz_defaults(id) VALUES($1)"
				var x fixture.Executor = backend.DB
				held := timezoneHeld(t, p, backend.DB, query)
				if prepared {
					x = held
				}
				for i, settings := range []struct{ zone, style string }{{"UTC", "ISO,MDY"}, {"America/New_York", "SQL,DMY"}} {
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("SET TIME ZONE '"+settings.zone+"'", 0))
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("SET DateStyle='"+settings.style+"'", 0))
					var insert fixture.Step
					if name == "literal" {
						insert = fixture.Exec(fmt.Sprintf("INSERT INTO tz_defaults(id) VALUES(%d)", i+1), 1)
					} else {
						insert = fixture.Exec(query, 1, i+1)
					}
					fixture.RunStep(t, p.Ctx, x, backend.Recorder, insert)
				}
			}
			cell := fixture.Exec("INSERT INTO tz_defaults VALUES(3,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT),(4,NULL,NULL,'explicit',NULL,NULL,NULL)", 2)
			cell.Prepared = prepared
			p.Step(t, cell)
			p.Step(t, fixture.Exec("SET DateStyle='ISO,MDY'", 0))
			p.Step(t, fixture.Select("SELECT id,(z AT TIME ZONE 'UTC')::TEXT AS z,(d AT TIME ZONE 'UTC')::TEXT AS d,s,b,k,n::TEXT AS n FROM tz_defaults ORDER BY id", []string{"id", "z", "d", "s", "b", "k", "n"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "TEXT"}, [][]any{
				{int64(1), "2004-07-01 12:00:00", "2004-10-19 00:00:00", "2004-10-19", false, false, "2004-10-19"},
				{int64(2), "2004-07-01 16:00:00", "2004-10-19 04:00:00", "19/10/2004", true, true, nil},
				{int64(3), "2004-07-01 16:00:00", "2004-10-19 04:00:00", "19/10/2004", true, true, nil},
				{int64(4), nil, nil, "explicit", nil, nil, nil},
			}))
			deferred := fixture.Exec("CREATE TABLE tz_deferred(id INTEGER,z TIMESTAMPTZ DEFAULT (TIMESTAMP '2004-07-01 12:00' AT TIME ZONE 'No/Such_Zone'))", 0)
			deferred.Prepared = prepared
			p.Step(t, deferred)
			p.Step(t, fixture.Exec("INSERT INTO tz_deferred VALUES(1,NULL)", 1))
			failure := fixture.ImplicitError("INSERT INTO tz_deferred(id) VALUES(2)", fixture.Diagnostic{Code: "22023", Message: `time zone "No/Such_Zone" not recognized`})
			failure.Prepared = prepared
			failure.ErrorStage = "execute"
			p.Step(t, failure)
			p.Step(t, fixture.Select("SELECT id,z::TEXT AS z FROM tz_deferred ORDER BY id", []string{"id", "z"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}}))
		})
	}
}
