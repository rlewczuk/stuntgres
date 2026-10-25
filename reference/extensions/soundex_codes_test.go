package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSoundexCodes(t *testing.T) {
	soundexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE EXTENSION fuzzystrmatch", 0))
		for _, tc := range []struct{ input, code string }{
			{"hello world!", "H464"}, {"Ann", "A500"}, {"Anne", "A500"}, {"Andrew", "A536"}, {"Margaret", "M626"},
			{"Robert", "R163"}, {"Rupert", "R163"}, {"Rubin", "R150"}, {"Smith", "S530"}, {"Smyth", "S530"},
			{"Ashcraft", "A226"}, {"Ashcroft", "A226"}, {"Pfister", "P236"}, {"Tymczak", "T522"}, {"Honeyman", "H555"},
		} {
			t.Run(tc.input, func(t *testing.T) {
				soundexFrames(t, p, b, c, "soundex", []struct { args []any; want any }{{[]any{tc.input}, tc.code}})
			})
		}
		for _, tc := range []struct { left, right string; score int64 }{
			{"Anne", "Ann", 4}, {"Anne", "Andrew", 2}, {"Anne", "Margaret", 0},
		} {
			soundexFrames(t, p, b, c, "difference", []struct { args []any; want any }{{[]any{tc.left, tc.right}, tc.score}})
		}
		run(fixture.Exec("CREATE TABLE sx_names(id int,name text)", 0))
		run(fixture.Exec("INSERT INTO sx_names VALUES(1,'Ann'),(2,'Anne'),(3,'Andrew'),(4,'Margaret'),(5,NULL)", 5))
		for _, prepared := range []bool{false, true} {
			for _, s := range []fixture.Step{
				fixture.Select("SELECT id FROM sx_names WHERE name='Ann' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}),
				fixture.Select("SELECT id FROM sx_names WHERE soundex(name)=soundex('Ann') ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}),
				fixture.Select("SELECT id,soundex(name),difference(name,'Ann') FROM sx_names ORDER BY id", []string{"id", "soundex", "difference"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(1), "A500", int64(4)}, {int64(2), "A500", int64(4)}, {int64(3), "A536", int64(2)}, {int64(4), "M626", int64(0)}, {int64(5), nil, nil}}),
			} {
				s.Prepared = prepared
				run(s)
			}
		}
	})
}
