package parser

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

func TestStatisticsObjectOwnerReplayParser(t *testing.T) {
	for _, prefix := range []string{"", "/* é界 */ "} {
		q := prefix + `ALTER STATISTICS "schema"."statistics" OWNER TO "recipient"`
		for _, enabled := range []bool{false, true, false} {
			statements, err := ParseWithOptions(q, Options{StatisticsOwnerReplay: enabled})
			if !enabled {
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "statement is unsupported", Position: strconv.Itoa(utf8.RuneCountInString(prefix) + 1)}
				var got *sqlstate.Error
				if len(statements) != 0 || !errors.As(err, &got) || *got != want {
					t.Fatal("ordinary ALTER escaped boundary", statements, err)
				}
				continue
			}
			if err != nil || len(statements) != 1 {
				t.Fatal(statements, err)
			}
			raw, ok := statements[0].(*AlterOwner)
			if !ok || raw.Kind != "statistics" || len(raw.Name.Parts) != 2 || raw.Name.Parts[0].Text != "schema" || raw.Name.Parts[1].Text != "statistics" || raw.Target.Name != "recipient" || raw.Target.Identity != "" || raw.Pos.Character != utf8.RuneCountInString(prefix)+1 {
				t.Fatal("replay AST lost identity/position", raw)
			}
		}
		for _, tc := range []struct{ q, token string }{
			{"ALTER STATISTICS s OWNER TO", ""},
			{"ALTER STATISTICS s OWNER TO recipient garbage", "garbage"},
			{"ALTER STATISTICS s OWNER TO recipient; DROP STATISTICS", ""},
		} {
			source := prefix + tc.q
			statements, err := ParseWithOptions(source, Options{StatisticsOwnerReplay: true})
			offset := len(source)
			message := "syntax error at end of input"
			if tc.token != "" {
				offset = strings.LastIndex(source, tc.token)
				message = `syntax error at or near "` + tc.token + `"`
			}
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: message, Position: strconv.Itoa(utf8.RuneCountInString(source[:offset]) + 1)}
			var got *sqlstate.Error
			if len(statements) != 0 || !errors.As(err, &got) || *got != want {
				t.Fatal("replay parsed malformed prefix", statements, err, want)
			}
		}
	}
}

func TestStatisticsNDistinctTargetParser(t *testing.T) {
	for _, replay := range []bool{false, true, false} {
		for _, tc := range []struct {
			value string
			want  int64
		}{{"DEFAULT", -1}, {"-1", -1}, {"0", 0}, {"+10001", 10001}, {"2147483647", 2147483647}} {
			source := `/* é */ ALTER STATISTICS IF EXISTS "s"."object" SET STATISTICS ` + tc.value
			statements, err := ParseWithOptions(source, Options{StatisticsOwnerReplay: replay})
			if err != nil || len(statements) != 1 {
				t.Fatal(statements, err)
			}
			s, ok := statements[0].(*AlterStatisticsTarget)
			if !ok || !s.IfExists || s.Target != tc.want || len(s.Name.Parts) != 2 || s.Name.Parts[0].Text != "s" || s.Name.Parts[1].Text != "object" || s.Pos.Character != 9 {
				t.Fatal(s)
			}
		}
	}
	for _, magnitude := range []string{"2147483648", "-2147483648", "+2147483648", "-2147483649", "1.5"} {
		source := "ALTER STATISTICS s SET STATISTICS " + magnitude
		statements, err := Parse(source)
		var diagnostic *sqlstate.Error
		if len(statements) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
			t.Fatal(statements, err)
		}
	}
	source := "ALTER STATISTICS s SET STATISTICS 0 trailing"
	if statements, err := Parse(source); len(statements) != 0 || err == nil {
		t.Fatal("accepted valid prefix", statements, err)
	}
}
