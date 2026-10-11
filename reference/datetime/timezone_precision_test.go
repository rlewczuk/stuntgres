package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceTimezonePrecisionDeclarationsAndOccurrenceSizing(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("SET TIME ZONE 'UTC'", 0))
	p.Step(t, fixture.Exec("CREATE TABLE tz_precision(z TIMESTAMPTZ,z0 TIMESTAMPTZ(0),z3 TIMESTAMP(3) WITH TIME ZONE,z6 TIMESTAMPTZ(6),q TIMETZ,q0 TIMETZ(0),q3 TIME(3) WITH TIME ZONE,q6 TIMETZ(6))", 0))
	for _, backend := range p.Backends() {
		query := "INSERT INTO tz_precision VALUES($1,$1,$1,$1,$2,$2,$2,$2)"
		held := timezoneHeld(t, p, backend.DB, query)
		fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(query, 1, "2004-01-01 00:00:00.123456+02", "12:00:00.123456-05"))
		want := [][]any{{"2003-12-31 22:00:00.123456+00", "2003-12-31 22:00:00+00", "2003-12-31 22:00:00.123+00", "2003-12-31 22:00:00.123456+00", "12:00:00.123456-05", "12:00:00-05", "12:00:00.123-05", "12:00:00.123456-05"}}
		if backend.PG {
			want[0][4] = time.Date(0, 1, 1, 17, 0, 0, 123456000, time.UTC)
			want[0][5] = time.Date(0, 1, 1, 17, 0, 0, 0, time.UTC)
			want[0][6] = time.Date(0, 1, 1, 17, 0, 0, 123000000, time.UTC)
			want[0][7] = time.Date(0, 1, 1, 17, 0, 0, 123456000, time.UTC)
		}
		s := fixture.Select("SELECT z::TEXT AS z,z0::TEXT AS z0,z3::TEXT AS z3,z6::TEXT AS z6,q,q0,q3,q6 FROM tz_precision", []string{"z", "z0", "z3", "z6", "q", "q0", "q3", "q6"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TIMETZ", "TIMETZ", "TIMETZ", "TIMETZ"}, want)
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(s))
	}
}

func TestReferenceTimezoneFiniteEndpointsAndWideDateComparison(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("SET TIME ZONE 'UTC'", 0))
	p.Step(t, fixture.Exec("CREATE TABLE tz_endpoints(v TIMESTAMPTZ)", 0))
	p.Step(t, fixture.Exec("INSERT INTO tz_endpoints VALUES('4714-11-24 00:00+00 BC'),('294276-12-31 23:59:59.999999+00')", 2))
	timezoneBoth(t, p, fixture.Select("SELECT v::TEXT AS v FROM tz_endpoints ORDER BY tz_endpoints.v", []string{"v"}, []string{"TEXT"}, [][]any{{"4714-11-24 00:00:00+00 BC"}, {"294276-12-31 23:59:59.999999+00"}}))
	timezoneBoth(t, p, fixture.Select("SELECT DATE '5874897-12-31'>v AS a,v<DATE '5874897-12-31' AS b FROM tz_endpoints", []string{"a", "b"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}, {true, true}}))
	for _, input := range []string{"4714-11-24 00:00+01 BC", "294276-12-31 23:59:59.999999-01"} {
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError("INSERT INTO tz_endpoints VALUES($1)", fixture.Diagnostic{Code: "22008", Message: `timestamp out of range: "` + input + `"`, Context: "unnamed portal parameter $1 = '...'"}, input)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			p.Step(t, s)
			timezoneBoth(t, p, fixture.Select("SELECT count(*) AS n FROM tz_endpoints", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
		}
	}
}
