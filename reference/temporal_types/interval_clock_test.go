package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIntervalClockEndpointControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		run(fixture.Exec("CREATE TABLE iv(v INTERVAL)", 0))
		for _, tc := range []struct{ input, out string }{
			{"-2562047788:00:54.775807", "-2562047788:00:54.775807"},
			{"2562047788:00:54.775807", "2562047788:00:54.775807"},
			{"+2562047788:00:54.775807", "2562047788:00:54.775807"},
			{"-9223372036854775808 microseconds", "-2562047788:00:54.775808"},
			{"+9223372036854775807 microseconds", "2562047788:00:54.775807"},
		} {
			for _, prepared := range []bool{false, true} {
				for _, parameter := range []bool{false, true} {
					s := fixture.Exec("INSERT INTO iv VALUES('"+tc.input+"')", 1)
					if parameter {
						s = fixture.Exec("INSERT INTO iv VALUES($1)", 1, tc.input)
					}
					s.Prepared = prepared
					run(s)
					s = fixture.Select("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{tc.out}})
					s.Prepared = prepared
					run(s)
					s = fixture.Select("SELECT INTERVAL '"+tc.input+"' FROM iv", []string{"interval"}, []string{"INTERVAL"}, [][]any{{tc.out}})
					s.Prepared = prepared
					run(s)
					run(fixture.Exec("DELETE FROM iv", 1))
				}
			}
		}
	}
}

func TestReferenceIntervalClockEndpointSessionAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE iv(v INTERVAL)", 0))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO iv VALUES('1 second')", 1))
		a, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()
		b, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer b.Close()
		for _, own := range []struct{ name, out string }{
			{"postgres", "00:00:01"},
			{"postgres_verbose", "@ 1 sec"},
			{"sql_standard", "0:00:01"},
			{"iso_8601", "PT1S"},
		} {
			fixture.RunStep(t, p.Ctx, a, backend.Recorder, fixture.Exec("SET IntervalStyle="+own.name, 0))
			fixture.RunStep(t, p.Ctx, b, backend.Recorder, fixture.Exec("SET IntervalStyle=iso_8601", 0))
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("INSERT INTO iv VALUES($1),($2)", fixture.Diagnostic{Code: "22007", Message: "invalid input syntax for type interval: \"-2562047788:00:54.775808\""}, "1 second", "-2562047788:00:54.775808")
				s.Prepared = prepared
				s.ErrorStage = "execute"
				pg := *s.Diagnostic
				pg.Context = "unnamed portal parameter $2 = '...'"
				s.Diagnostic = &pg
				fixture.RunStep(t, p.Ctx, a, backend.Recorder, s)
				fixture.RunStep(t, p.Ctx, a, backend.Recorder, fixture.Select("SHOW IntervalStyle", []string{"IntervalStyle"}, []string{"TEXT"}, [][]any{{own.name}}))
				fixture.RunStep(t, p.Ctx, b, backend.Recorder, fixture.Select("SHOW IntervalStyle", []string{"IntervalStyle"}, []string{"TEXT"}, [][]any{{"iso_8601"}}))
				fixture.RunStep(t, p.Ctx, a, backend.Recorder, fixture.Select("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{own.out}}))
				fixture.RunStep(t, p.Ctx, b, backend.Recorder, fixture.Select("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{"PT1S"}}))
			}
		}
	}
}

func TestReferenceIntervalMinimumTimeDefaultsViews(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		run(fixture.Exec("CREATE TABLE iv(id INTEGER,v INTERVAL DEFAULT INTERVAL '-9223372036854775808 microseconds')", 0))
		run(fixture.Exec("INSERT INTO iv(id) VALUES(1)", 1))
		run(fixture.Exec("CREATE VIEW ivview AS SELECT INTERVAL '-9223372036854775808 microseconds' AS v FROM iv WHERE id=1", 0))
		for _, style := range []struct{ name, out string }{
			{"postgres", "-2562047788:00:54.775808"},
			{"postgres_verbose", "@ 2562047788 hours 54.775808 secs ago"},
			{"sql_standard", "-2562047788:00:54.775808"},
			{"iso_8601", "PT-2562047788H-54.775808S"},
		} {
			run(fixture.Exec("SET IntervalStyle="+style.name, 0))
			for _, prepared := range []bool{false, true} {
				for _, q := range []string{"SELECT v FROM iv", "SELECT v FROM ivview", "SELECT INTERVAL '-9223372036854775808 microseconds' AS v FROM iv"} {
					s := fixture.Select(q, []string{"v"}, []string{"INTERVAL"}, [][]any{{style.out}})
					s.Prepared = prepared
					run(s)
				}
				s := fixture.Exec("INSERT INTO iv(id) VALUES(2)", 1)
				s.Prepared = prepared
				run(s)
				run(fixture.Select("SELECT v FROM iv WHERE id=2", []string{"v"}, []string{"INTERVAL"}, [][]any{{style.out}}))
				run(fixture.Exec("DELETE FROM iv WHERE id=2", 1))
			}
		}
	}
}
