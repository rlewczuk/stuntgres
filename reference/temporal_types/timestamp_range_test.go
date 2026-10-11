package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceTimestampTimeParameterRangeDiagnostics(t *testing.T) {
	p := newTimestampReferenceScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.TemporalRunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(v TIMESTAMP NOT NULL)", 0))
			run(fixture.Exec("INSERT INTO t VALUES('epoch')", 1))
			unchanged := fixture.Select("SELECT v FROM t", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}})
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
					step := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message}, tc.input)
					pg := *step.Diagnostic
					pg.Context = "unnamed portal parameter $1 = '...'"
					step.Diagnostic = &pg
					step.ErrorStage = "execute"
					run(step)
					run(unchanged)
					run(fixture.Prepared(step))
					run(unchanged)
				}
			}
		})
	}
}

func TestReferenceTimestampTimeDisplacementBounds(t *testing.T) {
	p := newTimestampReferenceScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.TemporalRunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(v TIMESTAMP)", 0))
			query := "INSERT INTO t VALUES($1)"
			stmt, err := b.DB.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := stmt.Close(); err != nil {
					t.Error(err)
				}
			}()
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
			for _, offset := range []int{57599, -57599} {
				input := time.Date(2004, 10, 19, 10, 23, 54, 123456600, time.FixedZone("civil", offset))
				step := fixture.Exec(query, 1, input)
				run(step)
				fixture.TemporalRunStep(t, p.Ctx, held, b.Recorder, step)
			}
			want := time.Date(2004, 10, 19, 10, 23, 54, 123457000, time.UTC)
			run(fixture.Select("SELECT v FROM t", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{want}, {want}, {want}, {want}}))
		})
	}
}
