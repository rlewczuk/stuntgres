package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceMetaphoneCodes(t *testing.T) {
	metaphoneCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE EXTENSION fuzzystrmatch", 0))
		metaphoneFrames(t, p, b, c, "metaphone", []struct {
			args []any
			want any
		}{
			{[]any{"GUMBO", 4}, "KM"}, {[]any{"Smith", 8}, "SM0"}, {[]any{"Schmidt", 8}, "SKMTT"},
			{[]any{"", 8}, ""}, {[]any{nil, 8}, nil}, {[]any{"gumbo", 1}, "K"},
		})
		for _, tc := range []struct {
			name   string
			values []any
		}{
			{"dmetaphone", []any{"SM0", "XMT", "", nil, "KMP", "S", "S", "PKLK"}},
			{"dmetaphone_alt", []any{"XMT", "SMT", "", nil, "KMP", "X", "S", "PKKS"}},
		} {
			var frames []struct {
				args []any
				want any
			}
			// A late Germanic marker changes the earlier SIO branch. In bagallegos,
			// primary fills first; alternate still needs the final S.
			for i, input := range []any{"Smith", "Schmidt", "", nil, "gumbo", "SIO" + strings.Repeat("!", 300), "SIO" + strings.Repeat("!", 300) + "W", "bagallegos"} {
				frames = append(frames, struct {
					args []any
					want any
				}{[]any{input}, tc.values[i]})
			}
			metaphoneFrames(t, p, b, c, tc.name, frames)
		}
		for _, tc := range []struct{ input, want string }{
			{"phone", "FN"}, {"whale", "HL"}, {"christ", "KRST"}, {"school", "SKL"},
			{"Schwartz", "XWRTS"}, {"AE", "E"}, {"!!AE", "E"}, {"X", "S"},
			{"ch", "X"}, {"c-h", "K"}, {"éa", "A"}, {"123!?", ""},
		} {
			t.Run("single_"+tc.input, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, metaphoneSelect("SELECT metaphone($1,8)", "metaphone", tc.want, tc.input))
			})
		}
		for _, tc := range []struct{ input, primary, alternate string }{
			{"a", "A", "A"}, {"y", "A", "A"}, {"x", "S", "S"}, {"j", "J", "A"},
			{"aj", "AJ", "A"}, {"Jackson", "JKSN", "AKSN"}, {"Jose", "HS", "HS"},
			{"Josef", "JSF", "HSF"}, {"Rogier", "RJ", "RJR"}, {"Washington", "AXNK", "FXNK"},
			{"123!?", "", ""}, {"éa", "", ""}, {"Ç", "", ""}, {"Ñ", "", ""},
		} {
			t.Run("double_"+tc.input, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT dmetaphone($1),dmetaphone_alt($1)", []string{"dmetaphone", "dmetaphone_alt"}, []string{"TEXT", "TEXT"}, [][]any{{tc.primary, tc.alternate}}, tc.input))
			})
		}
		run(fixture.Exec("CREATE TABLE phon_codes(id int,name text)", 0))
		run(fixture.Exec("INSERT INTO phon_codes VALUES(1,'Smith'),(2,'Schmidt'),(3,'gumbo'),(4,''),(5,NULL)", 5))
		for _, s := range []fixture.Step{
			fixture.Select("SELECT id,dmetaphone(name),dmetaphone_alt(name) FROM phon_codes ORDER BY id", []string{"id", "dmetaphone", "dmetaphone_alt"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "SM0", "XMT"}, {int64(2), "XMT", "SMT"}, {int64(3), "KMP", "KMP"}, {int64(4), "", ""}, {int64(5), nil, nil}}),
			fixture.Select("SELECT id FROM phon_codes WHERE dmetaphone(name)='SM0' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}),
			fixture.Select("SELECT id FROM phon_codes WHERE dmetaphone_alt(name)='SMT' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}),
			fixture.Select("SELECT metaphone(name,8) AS m,dmetaphone(name) AS p,dmetaphone_alt(name) AS a FROM phon_codes WHERE false", []string{"m", "p", "a"}, []string{"TEXT", "TEXT", "TEXT"}, nil),
		} {
			run(s)
			run(fixture.Prepared(s))
		}
	})
}
