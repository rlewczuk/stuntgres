package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDatetimeMakeWriteFailures(t *testing.T) {
	datetimeMakeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE dt_make_write(id integer,iv interval NOT NULL,z timestamptz)", 0))
		run(fixture.Exec("INSERT INTO dt_make_write VALUES(1,make_interval(days=>1),make_timestamptz(2000,1,1,0,0,0,'UTC'))", 1))
		cases := []struct {
			name, query string
			diagnostic  fixture.Diagnostic
		}{
			{"zone", "INSERT INTO dt_make_write VALUES(2,make_interval(days=>2),make_timestamptz(2000,1,1,0,0,0,'UTC')),(3,make_interval(days=>3),make_timestamptz(2000,1,1,0,0,0,'Nehwon/Lankhmar'))", datetimeMakeError("22023", `time zone "Nehwon/Lankhmar" not recognized`)},
			{"overflow", "INSERT INTO dt_make_write VALUES(2,make_interval(days=>2),NULL),(3,make_interval(years=>178956971),NULL)", datetimeMakeError("22008", "interval out of range")},
			{"runtimeCoercion", "INSERT INTO dt_make_write VALUES(2,make_interval(days=>$1::text::integer),NULL),(3,make_interval(days=>$2::text::integer),NULL)", datetimeMakeError("22P02", `invalid input syntax for type integer: "bad"`)},
			{"notNull", "INSERT INTO dt_make_write VALUES(2,make_interval(days=>2),NULL),(3,make_interval(secs=>NULL),NULL)", func() fixture.Diagnostic {
				d := datetimeMakeError("23502", `null value in column "iv" of relation "dt_make_write" violates not-null constraint`)
				d.Detail = "Failing row contains (3, null, null)."
				d.Schema = "public"
				d.Table = "dt_make_write"
				d.Column = "iv"
				return d
			}()},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(tc.query, tc.diagnostic)
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if tc.name == "runtimeCoercion" {
						s.Args = []any{"2", "bad"}
					}
					run(s)
					run(fixture.Select("SELECT id,iv,z::text AS z FROM dt_make_write ORDER BY id", []string{"id", "iv", "z"}, []string{"INT4", "INTERVAL", "TEXT"}, [][]any{{int64(1), "1 day", "2000-01-01 00:00:00+00"}}))
				}
			})
		}
	})
}
