package parser

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

func TestStatisticsAlterParser(t *testing.T) {
	for _, prefix := range []string{"", "/* é界 */ "} {
		for _, operand := range []string{`"recipient"`, "CURRENT_ROLE", "CURRENT_USER", "SESSION_USER"} {
			q := prefix + `ALTER STATISTICS "schema"."statistics" OWNER TO ` + operand
			statements, err := Parse(q)
			if err != nil || len(statements) != 1 {
				t.Fatal(statements, err)
			}
			raw, ok := statements[0].(*AlterOwner)
			if !ok || raw.Kind != "statistics" || len(raw.Name.Parts) != 2 || raw.Name.Parts[0].Text != "schema" || raw.Name.Parts[1].Text != "statistics" || raw.Pos.Character != utf8.RuneCountInString(prefix)+1 {
				t.Fatal("ordinary AST lost identity/position", raw)
			}
			if operand == `"recipient"` && raw.Target.Name != "recipient" {
				t.Fatal(raw)
			}
			wantIdentity := ""
			if operand == "CURRENT_ROLE" || operand == "CURRENT_USER" {
				wantIdentity = "current"
			}
			if operand == "SESSION_USER" {
				wantIdentity = "session"
			}
			if raw.Target.Identity != wantIdentity || raw.Target.Pos.Character != utf8.RuneCountInString(q[:strings.LastIndex(q, operand)])+1 {
				t.Fatal("owner operand position/identity", raw)
			}
		}
		for _, action := range []string{`RENAME TO "renamed"`, `SET SCHEMA "destination"`} {
			statements, err := Parse(prefix + `ALTER STATISTICS "schema"."statistics" ` + action)
			if err != nil || len(statements) != 1 {
				t.Fatal(statements, err)
			}
			switch raw := statements[0].(type) {
			case *AlterStatisticsRename:
				if raw.NewName.Text != "renamed" || raw.Pos.Character != utf8.RuneCountInString(prefix)+1 {
					t.Fatal(raw)
				}
			case *AlterStatisticsSchema:
				if raw.Schema.Text != "destination" || raw.Pos.Character != utf8.RuneCountInString(prefix)+1 {
					t.Fatal(raw)
				}
			default:
				t.Fatal(statements)
			}
		}
		for _, tc := range []struct{ q, token string }{
			{"ALTER STATISTICS s OWNER TO", ""},
			{"ALTER STATISTICS s OWNER TO recipient garbage", "garbage"},
			{"ALTER STATISTICS s OWNER TO recipient; DROP STATISTICS", ""},
			{"ALTER STATISTICS IF EXISTS s OWNER TO recipient", "OWNER"},
			{"ALTER STATISTICS IF EXISTS s SET SCHEMA destination", "SCHEMA"},
			{"ALTER STATISTICS s RENAME TO", ""},
			{"ALTER STATISTICS s SET SCHEMA destination garbage", "garbage"},
		} {
			source := prefix + tc.q
			statements, err := Parse(source)
			offset := len(source)
			message := "syntax error at end of input"
			if tc.token != "" {
				offset = strings.LastIndex(source, tc.token)
				message = `syntax error at or near "` + tc.token + `"`
			}
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: message, Position: strconv.Itoa(utf8.RuneCountInString(source[:offset]) + 1)}
			var got *sqlstate.Error
			if len(statements) != 0 || !errors.As(err, &got) || *got != want {
				t.Fatal("parsed malformed utility prefix", statements, err, want)
			}
		}
	}
}

func TestStatisticsNDistinctTargetParser(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  int64
	}{{"DEFAULT", -1}, {"-1", -1}, {"0", 0}, {"+10001", 10001}, {"2147483647", 2147483647}} {
		source := `/* é */ ALTER STATISTICS IF EXISTS "s"."object" SET STATISTICS ` + tc.value
		statements, err := Parse(source)
		if err != nil || len(statements) != 1 {
			t.Fatal(statements, err)
		}
		s, ok := statements[0].(*AlterStatisticsTarget)
		if !ok || !s.IfExists || s.Target != tc.want || len(s.Name.Parts) != 2 || s.Name.Parts[0].Text != "s" || s.Name.Parts[1].Text != "object" || s.Pos.Character != 9 {
			t.Fatal(s)
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
