package parser

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestQueryWindowRangePositionedAST(t *testing.T) {
	q := "/*é🙂*/ SELECT count(*) OVER(ORDER BY k RANGE BETWEEN 1 PRECEDING AND 2 FOLLOWING) FROM t"
	statements, err := Parse(q)
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	window := statements[0].(*Select).Targets[0].(AggregateExpr).Window
	frame := window.Frame
	char := func(token string) int { return utf8.RuneCountInString(q[:strings.Index(q, token)]) + 1 }
	if frame == nil || frame.Pos.Character != char("RANGE") || frame.Start.Kind != FrameOffsetPreceding || frame.End.Kind != FrameOffsetFollowing || frame.Start.Pos.Character != char("1 PRECEDING") || frame.End.Pos.Character != char("2 FOLLOWING") {
		t.Fatal(frame)
	}
	offsets := window.Offsets()
	if offsets[0] != frame.Start.Offset || offsets[1] != frame.End.Offset || offsets[0].SourcePosition().Character != char("1 PRECEDING") || offsets[1].SourcePosition().Character != char("2 FOLLOWING") {
		t.Fatal(offsets)
	}
	statements, err = Parse("SELECT count(*) OVER(ORDER BY k RANGE 1 PRECEDING),row_number() OVER(ORDER BY k RANGE CURRENT ROW) FROM t")
	if err != nil {
		t.Fatal(err)
	}
	a := statements[0].(*Select).Targets[0].(AggregateExpr).Window
	b := statements[0].(*Select).Targets[1].(AggregateExpr).Window
	if a.Frame == b.Frame || a.Frame.End.Kind != FrameCurrentRow || a.Frame.End.Offset != nil || b.Frame.Start.Kind != FrameCurrentRow {
		t.Fatal(a, b)
	}
	a.Frame.Start.Kind = FrameUnboundedPreceding
	if b.Frame.Start.Kind != FrameCurrentRow {
		t.Fatal("shared writable frame")
	}
	for _, q := range []string{
		"SELECT count(*) OVER(ORDER BY k RANGE BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) FROM t",
		"SELECT count(*) OVER(ORDER BY k RANGE BETWEEN 2 PRECEDING AND 1 PRECEDING) FROM t",
		"SELECT count(*) OVER(ORDER BY k RANGE BETWEEN 1 FOLLOWING AND 2 FOLLOWING) FROM t",
		"SELECT count(*) OVER(ORDER BY k RANGE 1::range PRECEDING) FROM t",
		"SELECT count(*) OVER(ORDER BY k RANGE 1::public.range PRECEDING) FROM t",
	} {
		if _, err := Parse(q); err != nil {
			t.Fatal(q, err)
		}
	}
}

func TestQueryWindowRangeSourcesVisitBothOffsets(t *testing.T) {
	q := "SELECT count(*) OVER(ORDER BY id RANGE BETWEEN (SELECT n FROM inline_start) PRECEDING AND (SELECT n FROM inline_end) FOLLOWING) FROM base WINDOW unused AS(ORDER BY id RANGE BETWEEN (SELECT n FROM named_start) PRECEDING AND (SELECT n FROM named_end) FOLLOWING)"
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
