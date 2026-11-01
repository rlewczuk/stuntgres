package parser

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

func TestParseTriggerDeferred(t *testing.T) {
	for _, tc := range []struct {
		properties                            string
		deferrable, deferred, defSet, initSet bool
	}{
		{"", false, false, false, false},
		{"DEFERRABLE", true, false, true, false},
		{"INITIALLY DEFERRED", true, true, false, true},
		{"NOT DEFERRABLE INITIALLY IMMEDIATE", false, false, true, true},
		{"DEFERRABLE DEFERRABLE INITIALLY DEFERRED INITIALLY DEFERRED", true, true, true, true},
	} {
		q := `CREATE CONSTRAINT TRIGGER "名" AFTER INSERT OR UPDATE OF x OR DELETE ON s.t FROM s.ref ` + tc.properties + ` FOR EACH ROW WHEN(NEW.x > 0) EXECUTE PROCEDURE s.fn('a',NULL)`
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(q, err)
		}
		s, ok := statements[0].(*CreateTrigger)
		if len(statements) != 1 || !ok || !s.Constraint || s.Replace || !s.Row || s.Timing != "after" || s.Name.Text != "名" || len(s.Events) != 3 || s.Events[1].Columns[0].Text != "x" || s.When == nil || s.Arguments[0] != "a" || s.Arguments[1] != "null" || len(s.From.Parts) != 2 || s.From.Parts[1].Text != "ref" || s.Function.Parts[1].Text != "fn" {
			t.Fatalf("lost constraint trigger syntax: %#v", s)
		}
		if s.Deferrable != tc.deferrable || s.InitiallyDeferred != tc.deferred || s.DeferrabilitySet != tc.defSet || s.InitiallySet != tc.initSet {
			t.Fatalf("lost properties: %#v", s)
		}
		at := strings.Index(q, "FROM")
		if s.FromPos.Offset != at || s.FromPos.Character != utf8.RuneCountInString(q[:at])+1 || tc.defSet && s.DeferrabilityPos.Character == 0 || tc.initSet && s.InitiallyPos.Character == 0 {
			t.Fatal("lost attribute positions", s)
		}
		if tc.defSet {
			token := "DEFERRABLE"
			if !tc.deferrable {
				token = "NOT DEFERRABLE"
			}
			at := strings.LastIndex(q, token)
			if s.DeferrabilityPos.Offset != at || s.DeferrabilityPos.Character != utf8.RuneCountInString(q[:at])+1 {
				t.Fatal("lost deferrability token", s)
			}
		}
		if tc.initSet {
			at := strings.LastIndex(q, "INITIALLY")
			if s.InitiallyPos.Offset != at || s.InitiallyPos.Character != utf8.RuneCountInString(q[:at])+1 {
				t.Fatal("lost initial timing token", s)
			}
		}
	}
	base := "CREATE CONSTRAINT TRIGGER c AFTER INSERT ON t "
	tail := " FOR EACH ROW EXECUTE FUNCTION f()"
	for _, tc := range []struct{ properties, message, token string }{
		{"DEFERRABLE NOT DEFERRABLE", "conflicting constraint properties", "NOT"},
		{"INITIALLY IMMEDIATE INITIALLY DEFERRED", "conflicting constraint properties", "INITIALLY"},
		{"NOT DEFERRABLE INITIALLY DEFERRED", "constraint declared INITIALLY DEFERRED must be DEFERRABLE", "INITIALLY"},
		{"INITIALLY DEFERRED NOT DEFERRABLE", "constraint declared INITIALLY DEFERRED must be DEFERRABLE", "NOT"},
	} {
		q := base + tc.properties + tail
		_, err := Parse(q)
		var diagnostic *sqlstate.Error
		at := strings.LastIndex(q, tc.token)
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != tc.message || diagnostic.Position != strconv.Itoa(utf8.RuneCountInString(q[:at])+1) {
			t.Fatal(tc.properties, err)
		}
	}
	for _, q := range []string{
		"CREATE CONSTRAINT TRIGGER c BEFORE INSERT ON t" + tail,
		"CREATE CONSTRAINT TRIGGER c INSTEAD OF INSERT ON t" + tail,
		`CREATE CONSTRAINT TRIGGER c "after" INSERT ON t` + tail,
		base + "FOR ROW EXECUTE FUNCTION f()",
		base + "FOR EACH STATEMENT EXECUTE FUNCTION f()",
		base + "EXECUTE FUNCTION f()",
		base + tail + " garbage",
		base + "FOR \"each\" ROW EXECUTE FUNCTION f()",
	} {
		_, err := Parse(q)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
			t.Fatal("accepted invalid constraint trigger", q, err)
		}
	}
	q := "CREATE OR REPLACE CONSTRAINT TRIGGER c AFTER INSERT ON t" + tail
	statements, err := Parse(q)
	if err != nil || !statements[0].(*CreateTrigger).Replace || !statements[0].(*CreateTrigger).Constraint {
		t.Fatal("lost semantic replacement rejection input", statements, err)
	}
}
