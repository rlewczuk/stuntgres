package parser

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

func TestPLpgSQLForeachParser(t *testing.T) {
	for _, slice := range []string{"", " SLICE 0", " SLICE 1"} {
		for _, expression := range []string{`a`, `(a)`, `coalesce(a, '{LOOP}'::text[])`, `$tag$LOOP$tag$::text[]`, `a[1:2]`, `"loop"`} {
			source := `<<"外">> DECLARE x text; BEGIN <<"é">> FOREACH x` + slice + ` IN ARRAY /*before*/ ` + expression + ` /*after*/ LOOP CONTINUE "é"; END LOOP "é"; RETURN 7; END "外"`
			raw, err := ParsePLpgSQL(source, PLReturnPolicy{})
			if err != nil {
				t.Fatal(source, err)
			}
			loop := raw.Statements[0].Foreach
			if loop == nil || len(loop.Targets) != 1 || loop.Body == nil || !loop.Body.IsLoop || loop.Body.Label.Text != "é" || loop.Body.EndLabel.Text != "é" || loop.SliceSpecified != (slice != "") || (loop.Slice == 1) != (slice == " SLICE 1") {
				t.Fatal("lost target/slice/label syntax", source)
			}
			value := loop.Source
			if PLExpressionSource(source, value.Start, value.End) != expression || source[value.Delimiter.Offset:value.Delimiter.Offset+4] != "LOOP" || value.Start.Character != utf8.RuneCountInString(source[:value.Start.Offset])+1 || value.End.Character != utf8.RuneCountInString(source[:value.End.Offset])+1 {
				t.Fatal("lost original expression boundaries", value)
			}
			before := *loop
			other, err := ParsePLpgSQL(`DECLARE x integer; y integer; BEGIN FOREACH x,y IN ARRAY a LOOP END LOOP; RETURN 0; END`, PLReturnPolicy{})
			if err != nil || len(other.Statements[0].Foreach.Targets) != 2 || !reflect.DeepEqual(before, *loop) {
				t.Fatal("retained parser ownership", err)
			}
		}
	}
	for _, fragment := range []string{"SLICE -1", "SLICE 1.0", "SLICE $1", "SLICE 2147483648", "SLICE", "SLICE 1 SLICE 1"} {
		if _, err := ParsePLpgSQL(`DECLARE x integer; BEGIN RETURN 7; FOREACH x `+fragment+` IN ARRAY a LOOP END LOOP; END`, PLReturnPolicy{}); err == nil {
			t.Fatal("accepted invalid unreachable slice", fragment)
		}
	}
	for _, source := range []string{
		`DECLARE x integer; BEGIN FOREACH x IN a LOOP END LOOP; END`,
		`DECLARE x integer; BEGIN FOREACH x IN ARRAY a LOOP END LOOP wrong; END`,
		`DECLARE x integer; BEGIN FOREACH x IN ARRAY (a LOOP END LOOP; END`,
		`DECLARE x integer; BEGIN FOREACH x IN ARRAY a LOOP END LOOP; END trailing`,
		`DECLARE x integer; BEGIN FOREACH x IN ARRAY a LOOP END LOOP; x:=1 + ; RETURN 0; END`,
	} {
		if _, err := ParsePLpgSQL(source, PLReturnPolicy{}); err == nil {
			t.Fatal("accepted malformed complete input", source)
		}
	}
	for _, tc := range []struct{ source, token string }{
		{`DECLARE x integer; BEGIN FOREACH x IN ARRAY '{1}'::integer[] END LOOP; RETURN 0; END`, "; RETURN"},
		{`DECLARE x integer; BEGIN FOREACH x IN ARRAY a LOOP END LOOP; nonsense RETURN 0; END`, "nonsense"},
		{`DECLARE x integer; BEGIN FOREACH x IN ARRAY a END LOOP END LOOP; RETURN 0; END`, "END LOOP"},
	} {
		_, err := ParsePLpgSQL(tc.source, PLReturnPolicy{})
		var diagnostic *sqlstate.Error
		offset := strings.Index(tc.source, tc.token)
		if !errors.As(err, &diagnostic) || diagnostic.Position != strconv.Itoa(utf8.RuneCountInString(tc.source[:offset])+1) {
			t.Fatal("FOREACH diagnostic provenance escaped its source/statement boundary", tc.source, err)
		}
	}
	for _, source := range []string{
		`DECLARE x integer; BEGIN FOREACH x IN ARRAY a END /* LOOP */ 'unterminated`,
		`DECLARE x integer; BEGIN FOREACH x IN ARRAY (a END LOOP;`,
	} {
		if _, err := ParsePLpgSQL(source, PLReturnPolicy{}); err == nil {
			t.Fatal("failure-only diagnostic probe admitted syntax", source)
		}
	}
	for _, source := range []string{
		`DECLARE slice integer; BEGIN slice:=1; FOREACH slice IN ARRAY a LOOP END LOOP; RETURN slice; END`,
		`DECLARE slice slice; BEGIN slice:=slice; RETURN 0; END`,
		`<<slice>> DECLARE x integer; BEGIN <<slice>> FOREACH x IN ARRAY a LOOP CONTINUE slice; END LOOP slice; RETURN slice.x; END slice`,
		`DECLARE r pair; BEGIN FOREACH r.slice SLICE 1 IN ARRAY a LOOP END LOOP; RETURN 0; END`,
		`DECLARE a integer[]; BEGIN a[1]:=1; RETURN 0; END`,
	} {
		if _, err := ParsePLpgSQL(source, PLReturnPolicy{}); err != nil {
			t.Fatal("unreserved SLICE or ordinary assignment syntax regressed", source, err)
		}
	}
	for _, target := range []string{"a[1]", "a[1:2]", "r.a[1]", "a[1],x", "x,a[1]"} {
		source := "DECLARE a integer[]; r record; x integer; BEGIN IF false THEN FOREACH " + target + " IN ARRAY a LOOP END LOOP; END IF; RETURN 0; END"
		if _, err := ParsePLpgSQL(source, PLReturnPolicy{}); err == nil {
			t.Fatal("FOREACH inherited general assignment subscripts", target)
		}
	}
	const separated = `DECLARE x integer; r record; BEGIN FOREACH x /* , */ , r.a SLICE 0 IN ARRAY a LOOP END LOOP; RETURN 0; END`
	raw, err := ParsePLpgSQL(separated, PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	loop := raw.Statements[0].Foreach
	if len(loop.TargetCommas) != 1 || loop.TargetCommas[0].Offset != strings.Index(separated, ", r.a") || loop.SlicePos.Offset != strings.Index(separated, "SLICE 0") {
		t.Fatal("FOREACH lost keyword/separator token provenance", loop)
	}
	// Contextual tokens cannot reserve ordinary SQL identifiers.
	if _, err := Parse(`SELECT foreach, slice FROM foreach`); err != nil {
		t.Fatal("FOREACH changed ordinary SQL keyword admission", err)
	}
}

func TestPLpgSQLForeachSliceConstants(t *testing.T) {
	for _, tc := range []struct {
		token string
		want  int64
	}{
		{"0", 0}, {"00", 0}, {"0_0", 0}, {"0x0", 0}, {"0o0", 0}, {"0b0", 0},
		{"1", 1}, {"01", 1}, {"0_1", 1}, {"0x1", 1}, {"0X1", 1}, {"0o1", 1}, {"0O1", 1}, {"0b1", 1}, {"0B1", 1},
		{"0x_1", 1}, {"0o_1", 1}, {"0b_1", 1}, {"0b1_0", 2},
		{"010", 10}, {"08", 8}, {"0_8", 8},
		{"2147483647", 2147483647}, {"2_147_483_647", 2147483647},
		{"0x7fff_ffff", 2147483647}, {"0o17_777_777_777", 2147483647},
		{"0b111_1111_1111_1111_1111_1111_1111_1111", 2147483647},
	} {
		source := "/* é */ DECLARE x integer; BEGIN FOREACH x SLICE " + tc.token + " IN ARRAY a LOOP END LOOP; RETURN 0; END"
		raw, err := ParsePLpgSQL(source, PLReturnPolicy{})
		if err != nil {
			t.Fatal("rejected integer constant", tc.token, err)
		}
		loop := raw.Statements[0].Foreach
		keyword := strings.Index(source, "SLICE")
		if loop == nil || !loop.SliceSpecified || int64(loop.Slice) != tc.want || !ValidPLForeachSlice(int64(loop.Slice)) || loop.SlicePos.Offset != keyword || loop.SlicePos.Character != utf8.RuneCountInString(source[:keyword])+1 {
			t.Fatal("integer value or source provenance changed", tc, loop)
		}
	}
	for _, token := range []string{"-1", "+1", "1.0", "1e0", "1E+0", "2147483648", "2_147_483_648", "0x80000000", "0o20000000000", "0b10000000000000000000000000000000"} {
		source := "DECLARE x integer; BEGIN FOREACH x SLICE " + token + " IN ARRAY a LOOP END LOOP; RETURN 0; END"
		if raw, err := ParsePLpgSQL(source, PLReturnPolicy{}); err == nil || raw != nil {
			t.Fatal("non-ICONST or out-of-range slice entered syntax", token, err)
		}
	}
	for _, level := range []int64{-1, 2147483648} {
		if ValidPLForeachSlice(level) {
			t.Fatal("parser-impossible SLICE admitted by shared invariant", level)
		}
	}
}

func TestPLpgSQLForeachCursorKeyword(t *testing.T) {
	for _, direction := range []struct {
		word string
		want CursorDirection
	}{{"FORWARD", CursorForward}, {"BACKWARD", CursorBackward}} {
		for _, count := range []string{" slice", ""} {
			t.Run(direction.word+map[bool]string{true: "/counted", false: "/omitted"}[count != ""], func(t *testing.T) {
				source := "/* é */ DECLARE slice integer:=1; c refcursor; BEGIN MOVE " + direction.word + count + " FROM c; END"
				raw, err := ParsePLpgSQL(source, PLReturnPolicy{})
				if err != nil {
					t.Fatal(err)
				}
				cursor := raw.Statements[0].Cursor
				if cursor == nil || cursor.Action != PLMoveCursor || cursor.Motion.Direction != direction.want || cursor.Motion.All || cursor.Motion.Counted != (count != "") {
					t.Fatal("lost MOVE direction or counted/default distinction", cursor)
				}
				variable, ok := cursor.Variable.(ColumnExpr)
				if !ok || len(variable.Name.Parts) != 1 || variable.Name.Parts[0].Text != "c" {
					t.Fatal("count variable displaced cursor destination", cursor.Variable)
				}
				motion := cursor.Motion
				if count == "" {
					if motion.Count != nil {
						t.Fatal("omitted count acquired an expression", motion)
					}
					return
				}
				column, ok := motion.Count.(ColumnExpr)
				if !ok || len(column.Name.Parts) != 1 || column.Name.Parts[0].Text != "slice" {
					t.Fatal("contextual SLICE was not the count expression", motion.Count)
				}
				start, end, delimiter := motion.CountStart, motion.CountEnd, motion.CountDelimiter
				if PLExpressionSource(source, start, end) != "slice" || start.Offset != strings.LastIndex(source, "slice") || end.Offset != start.Offset+len("slice") || delimiter.Offset != strings.Index(source, "FROM") ||
					start.Character != utf8.RuneCountInString(source[:start.Offset])+1 || end.Character != utf8.RuneCountInString(source[:end.Offset])+1 || delimiter.Character != utf8.RuneCountInString(source[:delimiter.Offset])+1 {
					t.Fatal("count expression lost original source or FROM delimiter", motion)
				}
			})
		}
	}
}
