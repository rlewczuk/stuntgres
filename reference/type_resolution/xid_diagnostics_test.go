package reference_test

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceXIDInputDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	xidReferenceBackends(t, p, func(t *testing.T, b fixture.Backend) {
		for _, typ := range []string{"xid", "cid", "xid8"} {
			table := "xid_bad_" + typ
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE "+table+"(v "+typ+")", 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO "+table+" VALUES('7')", 1))
			cases := []struct{ in, code string }{{"", "22P02"}, {" \t\r\v\f\n", "22P02"}, {"+", "22P02"}, {"-", "22P02"}, {"+ 1", "22P02"}, {"- 1", "22P02"}, {"08", "22P02"}, {"0x", "22P02"}, {"0b10", "22P02"}, {"0o10", "22P02"}, {"1_000", "22P02"}, {"1.0", "22P02"}, {"\u00a01", "22P02"}, {"1\u00a0", "22P02"}, {"18446744073709551616", "22003"}, {"18446744073709551616x", "22003"}, {"-18446744073709551616", "22003"}}
			if typ != "xid8" {
				cases = append(cases, struct{ in, code string }{"4294967296", "22003"}, struct{ in, code string }{"-2147483649", "22003"}, struct{ in, code string }{"4294967296x", "22P02"}, struct{ in, code string }{"18446744071562067967", "22003"})
			}
			for i, tc := range cases {
				t.Run(fmt.Sprintf("%s/%d", typ, i), func(t *testing.T) {
					message := `invalid input syntax for type ` + typ + `: "` + tc.in + `"`
					if tc.code == "22003" {
						message = `value "` + tc.in + `" is out of range for type ` + typ
					}
					q := "/*é*/ INSERT INTO " + table + " VALUES('1'),('" + tc.in + "')"
					pos := utf8.RuneCountInString(q[:strings.LastIndex(q, "('")]) + 2
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: message, Position: strconv.Itoa(pos)})
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					s = fixture.Prepared(s)
					s.ErrorStage = "prepare"
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					context := "unnamed portal parameter $1 = '...'"
					if tc.in == "" {
						context = "unnamed portal parameter $1 = ''"
					}
					s = fixture.ImplicitError("INSERT INTO "+table+" VALUES($1)", fixture.Diagnostic{Code: tc.code, Message: message, Context: context}, tc.in)
					s.ErrorStage = "execute"
					xidReferenceRoutes(t, p.Ctx, b, s)
				})
			}
			xidReferenceRoutes(t, p.Ctx, b, fixture.Select("SELECT v FROM "+table, []string{"v"}, []string{strings.ToUpper(typ)}, [][]any{{"7"}}))
			for _, q := range []string{"CREATE TABLE xid_mod_" + typ + "(v " + typ + "(3))", "SELECT NULL::" + typ + "(3)"} {
				pos := strings.Index(q, typ+"(3)") + 1
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: `type modifier is not allowed for type "` + typ + `"`, Position: strconv.Itoa(pos)})
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				s = fixture.Prepared(s)
				s.ErrorStage = "prepare"
				// PostgreSQL defers utility-statement modifier validation to execution.
				if strings.HasPrefix(q, "CREATE TABLE ") {
					s.ErrorStage = "execute"
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
	})
}
