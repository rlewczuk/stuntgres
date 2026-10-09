package reference_test

import (
	"testing"
	"time"
)

func TestReferenceTimestampTimeParameterRangeDiagnostics(t *testing.T) {
	p := newTimestampReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceTemporalRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE t(v TIMESTAMP NOT NULL)", 0))
			run(referenceExec("INSERT INTO t VALUES('epoch')", 1))
			unchanged := referenceSelect("SELECT v FROM t", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}})
			for _, tc := range []struct {
				input         time.Time
				code, message string
			}{
				{time.Date(294277, 1, 1, 0, 0, 0, 0, time.UTC), "22008", `timestamp out of range: "294277-01-01 00:00:00Z"`},
				{time.Date(294277, 1, 1, 4, 5, 6, 123456789, time.FixedZone("east", 7384)), "22008", `timestamp out of range: "294277-01-01 04:05:06.123456789+02:03:04"`},
				{time.Date(-4713, 11, 23, 4, 5, 6, 123456789, time.FixedZone("west", -7384)), "22008", `timestamp out of range: "4714-11-23 04:05:06.123456789-02:03:04 BC"`},
				{time.Date(294277, 1, 1, 4, 5, 6, 123456789, time.FixedZone("east", 9045)), "22008", `timestamp out of range: "294277-01-01 04:05:06.123456789+02:30:45"`},
				{time.Date(-5000, 1, 1, 0, 0, 0, 0, time.UTC), "22008", `timestamp out of range: "5001-01-01 00:00:00Z BC"`},
				{time.Date(2004, 10, 19, 10, 23, 54, 0, time.FixedZone("east", 57600)), "22009", `time zone displacement out of range: "2004-10-19 10:23:54+16:00"`},
				{time.Date(2004, 10, 19, 10, 23, 54, 0, time.FixedZone("west", -57600)), "22009", `time zone displacement out of range: "2004-10-19 10:23:54-16:00"`},
				{time.Date(294277, 1, 1, 0, 0, 0, 0, time.FixedZone("east", 57600)), "22009", `time zone displacement out of range: "294277-01-01 00:00:00+16:00"`},
				{time.Date(-5000, 1, 1, 0, 0, 0, 0, time.FixedZone("west", -57600)), "22009", `time zone displacement out of range: "5001-01-01 00:00:00-16:00 BC"`},
				{time.Date(2147483648, 1, 1, 0, 0, 0, 0, time.FixedZone("east", 57600)), "22008", `date/time field value out of range: "2147483648-01-01 00:00:00+16:00"`},
				{time.Date(-2147483647, 1, 1, 0, 0, 0, 0, time.FixedZone("west", -57600)), "22008", `date/time field value out of range: "2147483648-01-01 00:00:00-16:00 BC"`},
				{time.Date(2004, 10, 19, 10, 23, 54, 123456789, time.FixedZone("east", 57600)), "22009", `time zone displacement out of range: "2004-10-19 10:23:54.123456789+16:00"`},
				{time.Date(2004, 10, 19, 10, 23, 54, 123456789, time.FixedZone("west", -57600)), "22009", `time zone displacement out of range: "2004-10-19 10:23:54.123456789-16:00"`},
				{time.Date(294277, 1, 1, 0, 0, 0, 0, time.FixedZone("west", -57600)), "22009", `time zone displacement out of range: "294277-01-01 00:00:00-16:00"`},
				{time.Date(294277, 1, 1, 0, 0, 0, 0, time.FixedZone("east", 57599)), "22008", `timestamp out of range: "294277-01-01 00:00:00+15:59:59"`},
				{time.Date(294277, 1, 1, 0, 0, 0, 0, time.FixedZone("west", -57599)), "22008", `timestamp out of range: "294277-01-01 00:00:00-15:59:59"`},
			} {
				for _, q := range []string{"INSERT INTO t VALUES($1)", "INSERT INTO t VALUES('2000-01-01'),($1)"} {
					step := referenceImplicitError(q, referenceDiagnostic{Code: tc.code, Message: tc.message}, tc.input)
					pg := *step.diagnostic
					pg.Context = "unnamed portal parameter $1 = '...'"
					step.diagnostic = &pg
					step.errorStage = "execute"
					run(step)
					run(unchanged)
					run(referencePrepared(step))
					run(unchanged)
				}
			}
		})
	}
}

func TestReferenceTimestampTimeDisplacementBounds(t *testing.T) {
	p := newTimestampReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceTemporalRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE t(v TIMESTAMP)", 0))
			query := "INSERT INTO t VALUES($1)"
			stmt, err := b.db.PrepareContext(p.ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := stmt.Close(); err != nil {
					t.Error(err)
				}
			}()
			held := referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: query}
			for _, offset := range []int{57599, -57599} {
				input := time.Date(2004, 10, 19, 10, 23, 54, 123456600, time.FixedZone("civil", offset))
				step := referenceExec(query, 1, input)
				run(step)
				referenceTemporalRunStep(t, p.ctx, held, b.recorder, step)
			}
			want := time.Date(2004, 10, 19, 10, 23, 54, 123457000, time.UTC)
			run(referenceSelect("SELECT v FROM t", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{want}, {want}, {want}, {want}}))
		})
	}
}
