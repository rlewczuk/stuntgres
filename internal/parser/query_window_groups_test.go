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

func TestQueryWindowGroupsFrames(t *testing.T) {
	q := "/*é🙂*/ SELECT count(*) OVER(ORDER BY k GROUPS BETWEEN 1 PRECEDING AND 2 FOLLOWING) FROM t"
	statements, err := Parse(q)
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	window := statements[0].(*Select).Targets[0].(AggregateExpr).Window
	frame := window.Frame
	char := func(token string) int { return utf8.RuneCountInString(q[:strings.Index(q, token)]) + 1 }
	if frame == nil || frame.Unit != FrameGroups || frame.Pos.Character != char("GROUPS") || frame.Start.Kind != FrameOffsetPreceding || frame.End.Kind != FrameOffsetFollowing || frame.Start.Pos.Character != char("1 PRECEDING") || frame.End.Pos.Character != char("2 FOLLOWING") {
		t.Fatal(frame)
	}
	offsets := window.Offsets()
	if offsets[0] != frame.Start.Offset || offsets[1] != frame.End.Offset {
		t.Fatal(offsets)
	}
	statements, err = Parse("SELECT count(*) OVER(ORDER BY k GROUPS 1 PRECEDING),count(*) OVER(ORDER BY k RANGE 1 PRECEDING) FROM t")
	if err != nil {
		t.Fatal(err)
	}
	a := statements[0].(*Select).Targets[0].(AggregateExpr).Window
	b := statements[0].(*Select).Targets[1].(AggregateExpr).Window
	if a.Frame == b.Frame || a.Frame.Unit != FrameGroups || b.Frame.Unit != FrameRange || a.Frame.End.Kind != FrameCurrentRow || a.Frame.End.Offset != nil {
		t.Fatal(a, b)
	}
	a.Frame.Start.Kind = FrameUnboundedPreceding
	if b.Frame.Start.Kind != FrameOffsetPreceding {
		t.Fatal("shared writable frame")
	}
	for _, text := range []string{
		"SELECT count(*) OVER(ORDER BY k GROUPS BETWEEN 1 PRECEDING AND 2 PRECEDING) FROM t",
		"SELECT count(*) OVER(ORDER BY k GROUPS BETWEEN 2 FOLLOWING AND 1 FOLLOWING) FROM t",
		"SELECT count(*) OVER(ORDER BY k GROUPS 1::groups PRECEDING) FROM t",
		"SELECT groups FROM groups",
		"SELECT count(*) OVER(ORDER BY k GROUPS 1::public.groups PRECEDING) FROM t",
	} {
		if _, err := Parse(text); err != nil {
			t.Fatal(text, err)
		}
	}
	for _, tc := range []struct{ frame, message, marker string }{
		{"1 FOLLOWING", "frame starting from following row cannot end with current row", "1 FOLLOWING"},
		{"BETWEEN 1 FOLLOWING AND CURRENT ROW", "frame starting from following row cannot have preceding rows", "CURRENT"},
	} {
		q := "/*é*/ SELECT count(*) OVER(ORDER BY k GROUPS " + tc.frame + ") FROM t"
		statements, err := Parse(q)
		var d *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P20", Message: tc.message, Position: strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, tc.marker)]) + 1)}
		if len(statements) != 0 || !errors.As(err, &d) || *d != want {
			t.Fatal(statements, err, want)
		}
	}
	statements, err = Parse("SELECT count(*) OVER(ORDER BY k GROUPS 1 PRECEDING) FROM t; SELECT (")
	if err == nil || len(statements) != 0 {
		t.Fatal("partial parsed prefix", statements, err)
	}
}

func TestQueryWindowGroupsQualification(t *testing.T) {
	q := "SELECT count(*) OVER(ORDER BY id GROUPS BETWEEN (SELECT n FROM inline_start) PRECEDING AND (SELECT n FROM inline_end) FOLLOWING) FROM base WINDOW unused AS(ORDER BY id GROUPS BETWEEN (SELECT n FROM named_start) PRECEDING AND (SELECT n FROM named_end) FOLLOWING)"
	statements, err := Parse(q)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	err = VisitQuerySources(statements[0], nil, func(table *TableReference, _ *Select) error {
		names = append(names, table.Name.Parts[0].Text)
		return nil
	})
	if err != nil || !reflect.DeepEqual(names, []string{"base", "inline_start", "inline_end", "named_start", "named_end"}) {
		t.Fatal(names, err)
	}
}
