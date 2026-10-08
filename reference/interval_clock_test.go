//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceIntervalClockEndpointControls(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		db     *sql.DB
		record *referenceRecorder
	}{{p.memory, p.memoryRecord}, {p.postgres, p.postgresRecord}} {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.record, s) }
		run(referenceExec("CREATE TABLE iv(v INTERVAL)", 0))
		for _, tc := range []struct{ input, out string }{
			{"-2562047788:00:54.775807", "-2562047788:00:54.775807"},
			{"2562047788:00:54.775807", "2562047788:00:54.775807"},
			{"+2562047788:00:54.775807", "2562047788:00:54.775807"},
			{"-9223372036854775808 microseconds", "-2562047788:00:54.775808"},
			{"+9223372036854775807 microseconds", "2562047788:00:54.775807"},
		} {
			for _, prepared := range []bool{false, true} {
				for _, parameter := range []bool{false, true} {
					s := referenceExec("INSERT INTO iv VALUES('"+tc.input+"')", 1)
					if parameter {
						s = referenceExec("INSERT INTO iv VALUES($1)", 1, tc.input)
					}
					s.prepared = prepared
					run(s)
					run(referenceSelect("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{tc.out}}))
					s = referenceSelect("SELECT INTERVAL '"+tc.input+"' FROM iv", []string{"interval"}, []string{"INTERVAL"}, [][]any{{tc.out}})
					s.prepared = prepared
					run(s)
					run(referenceExec("DELETE FROM iv", 1))
				}
			}
		}
	}
}

func TestReferenceIntervalClockEndpointSessionAtomicity(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		db     *sql.DB
		record *referenceRecorder
	}{{p.memory, p.memoryRecord}, {p.postgres, p.postgresRecord}} {
		referenceRunStep(t, p.ctx, backend.db, backend.record, referenceExec("CREATE TABLE iv(v INTERVAL)", 0))
		referenceRunStep(t, p.ctx, backend.db, backend.record, referenceExec("INSERT INTO iv VALUES('1 second')", 1))
		a, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()
		b, err := backend.db.Conn(p.ctx)
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
			referenceRunStep(t, p.ctx, a, backend.record, referenceExec("SET IntervalStyle="+own.name, 0))
			referenceRunStep(t, p.ctx, b, backend.record, referenceExec("SET IntervalStyle=iso_8601", 0))
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError("INSERT INTO iv VALUES($1),($2)", referenceDiagnostic{Code: "22007", Message: "invalid input syntax for type interval: \"-2562047788:00:54.775808\""}, "1 second", "-2562047788:00:54.775808")
				s.prepared = prepared
				s.errorStage = "execute"
				pg := *s.diagnostic
				pg.Context = "unnamed portal parameter $2 = '...'"
				s.diagnostic = &pg
				referenceRunStep(t, p.ctx, a, backend.record, s)
				referenceRunStep(t, p.ctx, a, backend.record, referenceSelect("SHOW IntervalStyle", []string{"IntervalStyle"}, []string{"TEXT"}, [][]any{{own.name}}))
				referenceRunStep(t, p.ctx, b, backend.record, referenceSelect("SHOW IntervalStyle", []string{"IntervalStyle"}, []string{"TEXT"}, [][]any{{"iso_8601"}}))
				referenceRunStep(t, p.ctx, a, backend.record, referenceSelect("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{own.out}}))
				referenceRunStep(t, p.ctx, b, backend.record, referenceSelect("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{"PT1S"}}))
			}
		}
	}
}

func TestReferenceIntervalMinimumTimeDefaultsViews(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		db     *sql.DB
		record *referenceRecorder
	}{{p.memory, p.memoryRecord}, {p.postgres, p.postgresRecord}} {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.record, s) }
		run(referenceExec("CREATE TABLE iv(id INTEGER,v INTERVAL DEFAULT INTERVAL '-9223372036854775808 microseconds')", 0))
		run(referenceExec("INSERT INTO iv(id) VALUES(1)", 1))
		run(referenceExec("CREATE VIEW ivview AS SELECT INTERVAL '-9223372036854775808 microseconds' AS v FROM iv WHERE id=1", 0))
		for _, style := range []struct{ name, out string }{
			{"postgres", "-2562047788:00:54.775808"},
			{"postgres_verbose", "@ 2562047788 hours 54.775808 secs ago"},
			{"sql_standard", "-2562047788:00:54.775808"},
			{"iso_8601", "PT-2562047788H-54.775808S"},
		} {
			run(referenceExec("SET IntervalStyle="+style.name, 0))
			for _, prepared := range []bool{false, true} {
				for _, q := range []string{"SELECT v FROM iv", "SELECT v FROM ivview", "SELECT INTERVAL '-9223372036854775808 microseconds' AS v FROM iv"} {
					s := referenceSelect(q, []string{"v"}, []string{"INTERVAL"}, [][]any{{style.out}})
					s.prepared = prepared
					run(s)
				}
				s := referenceExec("INSERT INTO iv(id) VALUES(2)", 1)
				s.prepared = prepared
				run(s)
				run(referenceSelect("SELECT v FROM iv WHERE id=2", []string{"v"}, []string{"INTERVAL"}, [][]any{{style.out}}))
				run(referenceExec("DELETE FROM iv WHERE id=2", 1))
			}
		}
	}
}
