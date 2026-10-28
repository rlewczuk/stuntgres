package parser

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
	"unicode/utf8"
)

func TestUsingIndexParserAttachments(t *testing.T) {
	for _, kind := range []string{"UNIQUE", "PRIMARY KEY"} {
		for _, named := range []bool{false, true} {
			for _, tail := range []string{"", " NOT DEFERRABLE", " INITIALLY IMMEDIATE", " NOT DEFERRABLE INITIALLY IMMEDIATE", " INITIALLY IMMEDIATE NOT DEFERRABLE", " NOT DEFERRABLE NOT DEFERRABLE", " INITIALLY IMMEDIATE INITIALLY IMMEDIATE", " DEFERRABLE", " INITIALLY DEFERRED", " DEFERRABLE INITIALLY DEFERRED"} {
				declaration := kind
				name := ""
				start := kind
				if named {
					declaration = `CONSTRAINT "é owned" ` + kind
					name = "é owned"
					start = "CONSTRAINT"
				}
				q := `ALTER TABLE 界 ADD ` + declaration + ` USING INDEX "pre built"` + tail
				statements, err := Parse(q)
				if err != nil || len(statements) != 1 {
					t.Fatal(q, statements, err)
				}
				table, ok := statements[0].(*AlterTable)
				if !ok || len(table.Actions) != 1 || table.Name.Parts[0].Text != "界" {
					t.Fatal(q, statements)
				}
				action := table.Actions[0]
				attachment := action.UsingIndex
				if action.Action != AddUsingIndex || attachment == nil || attachment.Name.Text != name || action.Constraint != attachment.Name || attachment.Index.Text != "pre built" || attachment.Primary != (kind == "PRIMARY KEY") {
					t.Fatal(q, action, attachment)
				}
				position := func(token string) int { return utf8.RuneCountInString(q[:strings.Index(q, token)]) + 1 }
				if action.Pos.Character != position("ADD") || attachment.Pos.Character != position(start) || attachment.KindPos.Character != position(kind) || attachment.Index.Pos.Character != position(`"pre built"`) {
					t.Fatal("lost source positions", q, action, attachment)
				}
				wantDeferred := strings.Contains(tail, "INITIALLY DEFERRED")
				wantDeferrable := wantDeferred || strings.Contains(tail, "DEFERRABLE") && !strings.Contains(tail, "NOT DEFERRABLE")
				if attachment.Deferrable != wantDeferrable || attachment.InitiallyDeferred != wantDeferred {
					t.Fatal("timing normalization lost", attachment)
				}
			}
		}
	}
	statements, err := Parse("ALTER TABLE t ADD UNIQUE USING INDEX idx, ALTER a SET NOT NULL")
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	actions := statements[0].(*AlterTable).Actions
	if len(actions) != 2 || actions[0].Action != AddUsingIndex || actions[1].Action != SetNotNull {
		t.Fatal(actions)
	}
}

func TestUsingIndexParserWholeInput(t *testing.T) {
	for _, kind := range []string{"UNIQUE", "PRIMARY KEY"} {
		prefix := "ALTER TABLE 界 ADD CONSTRAINT chosen " + kind + " USING INDEX idx "
		for _, tc := range []struct{ tail, code, message, token string }{
			{"NOT DEFERRABLE DEFERRABLE", "42601", "conflicting constraint properties", "DEFERRABLE"},
			{"DEFERRABLE NOT DEFERRABLE", "42601", "conflicting constraint properties", "NOT DEFERRABLE"},
			{"INITIALLY IMMEDIATE INITIALLY DEFERRED", "42601", "conflicting constraint properties", "INITIALLY DEFERRED"},
			{"NOT DEFERRABLE INITIALLY DEFERRED", "42601", "constraint declared INITIALLY DEFERRED must be DEFERRABLE", "INITIALLY"},
			{"nonsense", "42601", `syntax error at or near "nonsense"`, "nonsense"},
			{"DEFERRABLE nonsense", "42601", `syntax error at or near "nonsense"`, "nonsense"},
			{"NOT DEFERRABLE nonsense", "42601", `syntax error at or near "nonsense"`, "nonsense"},
			{"INCLUDE(a)", "42601", `syntax error at or near "INCLUDE"`, "INCLUDE"},
			{".qualified", "42601", `syntax error at or near "."`, "."},
		} {
			q := prefix + tc.tail
			offset := strings.Index(q, tc.token)
			if tc.code == "42601" && tc.message == "conflicting constraint properties" {
				offset = strings.LastIndex(q, tc.token)
			}
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: strconv.Itoa(utf8.RuneCountInString(q[:offset]) + 1)}
			statements, err := Parse(q)
			var diagnostic *sqlstate.Error
			if statements != nil || !errors.As(err, &diagnostic) || !reflect.DeepEqual(*diagnostic, want) {
				t.Fatal(q, statements, err, diagnostic, want)
			}
		}
	}
	// New ALTER support does not admit CREATE's existing-index spelling or
	// qualified source index names; full-input syntax also beats a deferred boundary.
	for _, q := range []string{"CREATE TABLE t(a INT,UNIQUE USING INDEX idx)", "ALTER TABLE t ADD UNIQUE USING INDEX public.idx", "ALTER TABLE t ADD UNIQUE USING INDEX idx DEFERRABLE; nonsense"} {
		statements, err := Parse(q)
		var diagnostic *sqlstate.Error
		if statements != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
			t.Fatal(q, statements, err)
		}
	}
}
