//go:build reference

package reference_test

import "testing"

func TestReferenceTimezoneReviewStableDefaults(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			p := timezoneReferencePair(t)
			ddl := referenceExec("CREATE TABLE tz_defaults(id INTEGER,z TIMESTAMPTZ DEFAULT (TIMESTAMP '2004-07-01 12:00' AT LOCAL),d TIMESTAMPTZ DEFAULT DATE '2004-10-19'::TIMESTAMPTZ,s TEXT DEFAULT DATE '2004-10-19'::TEXT,b BOOL DEFAULT (DATE '2004-10-19'=TIMESTAMPTZ '2004-10-19 04:00+00'),k BOOL DEFAULT CASE DATE '2004-10-19' WHEN TIMESTAMPTZ '2004-10-19 04:00+00' THEN TRUE ELSE FALSE END,n DATE DEFAULT NULLIF(DATE '2004-10-19',TIMESTAMPTZ '2004-10-19 04:00+00'))", 0)
			ddl.prepared = prepared
			p.step(t, ddl)
			insert := referenceExec("INSERT INTO tz_defaults(id) VALUES($1)", 1)
			insert.prepared = prepared
			insert.args = []any{1}
			p.step(t, insert)
			p.step(t, referenceExec("SET TIME ZONE 'America/New_York'", 0))
			p.step(t, referenceExec("SET DateStyle='SQL,DMY'", 0))
			insert.args = []any{2}
			p.step(t, insert)
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
			p.step(t, failure)
			p.step(t, referenceSelect("SELECT id,z::TEXT AS z FROM tz_deferred ORDER BY id", []string{"id", "z"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}}))
		})
	}
}
