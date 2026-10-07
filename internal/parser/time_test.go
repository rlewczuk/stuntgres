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

func TestTimeDeclarationsAndPositions(t *testing.T) {
	for _, decl := range []string{"TIME", "TIME WITHOUT TIME ZONE", "TIME(0)", "TIME(6) WITHOUT TIME ZONE", `"time"(-1)`, `U&"t\0069me"(3)`} {
		q := "/*é*/ CREATE TABLE time(v " + decl + ")"
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(q, err)
		}
		raw := statements[0].(*CreateTable).Elements[0].Column.TypeName
		if raw.Name != "time" || raw.Pos.Character != utf8.RuneCountInString(q[:strings.Index(q, decl)])+1 {
			t.Fatal(raw)
		}
		if strings.Contains(decl, "(") && (len(raw.Modifiers) != 1 || raw.Modifiers[0].Integer == nil) {
			t.Fatal("missing precision", raw)
		}
	}
	q := "/*é*/ SELECT TIME(3) WITHOUT TIME ZONE '24:00' FROM time"
	statements, err := Parse(q)
	if err != nil {
		t.Fatal(err)
	}
	raw := statements[0].(*Select).Targets[0].(TypedLiteralExpr)
	if raw.Text != "24:00" || raw.TypeName.Name != "time" || len(raw.TypeName.Modifiers) != 1 || raw.TypeName.Modifiers[0].Integer == nil || *raw.TypeName.Modifiers[0].Integer != 3 {
		t.Fatal(raw)
	}
	for _, tc := range []struct {
		pos    Position
		needle string
	}{{raw.TypeName.Pos, "TIME"}, {raw.Pos, "'24"}} {
		offset := strings.Index(q, tc.needle)
		if tc.pos.Offset != offset || tc.pos.Character != utf8.RuneCountInString(q[:offset])+1 {
			t.Fatal(tc)
		}
	}
	statements, err = Parse("CREATE TABLE time(time TIME); INSERT INTO time VALUES(TIME '24:00'); SELECT time FROM time")
	if err != nil || len(statements) != 3 {
		t.Fatal(statements, err)
	}
	if _, ok := statements[2].(*Select).Targets[0].(ColumnExpr); !ok {
		t.Fatal("TIME name eligibility lost")
	}
}

func TestTimePrecisionScannerAndBoundaries(t *testing.T) {
	for _, tc := range []struct{ number, token, primary string }{{"-1", "-", "syntax error"}, {"+1", "+", "syntax error"}, {"1.5", "1.5", "syntax error"}, {".5", ".5", "syntax error"}, {"1e+2", "1e+2", "syntax error"}, {"2147483648", "2147483648", "syntax error"}, {"1.5foo", "1.5foo", "trailing junk after numeric literal"}, {"1.5e+", "1.5e+", "trailing junk after numeric literal"}, {"1..2", "..", "syntax error"}, {"1_", "1_", "trailing junk after numeric literal"}} {
		q := "/*é*/ CREATE TABLE bad(v TIME(" + tc.number + "))"
		_, err := Parse(q)
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: tc.primary + " at or near \"" + tc.token + "\"", Position: strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, tc.token)]) + 1)}
		var got *sqlstate.Error
		if !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
			t.Fatal(q, got, err, want)
		}
	}
	for _, head := range []string{`"time"`, `U&"t\0069me"`} {
		for _, number := range []string{"1.5", ".5", "1e2", "2147483648", "999999999999999999999999"} {
			q := "CREATE TABLE t(v " + head + "(" + number + "))"
			statements, err := Parse(q)
			if err != nil {
				t.Fatal(q, err)
			}
			raw := statements[0].(*CreateTable).Elements[0].Column.TypeName
			if len(raw.Modifiers) != 1 || raw.Modifiers[0].Raw != number || raw.Modifiers[0].Integer != nil {
				t.Fatal(raw)
			}
		}
	}
	for _, number := range []string{"0x3", "0b11", "0o3", "0_3"} {
		statements, err := Parse("CREATE TABLE t(v TIME(" + number + "))")
		if err != nil {
			t.Fatal(err)
		}
		raw := statements[0].(*CreateTable).Elements[0].Column.TypeName
		if len(raw.Modifiers) != 1 || raw.Modifiers[0].Integer == nil || *raw.Modifiers[0].Integer != 3 {
			t.Fatal(raw)
		}
	}
	for _, q := range []string{"SELECT TIME '24:00'", "CREATE TABLE t(v TIME[])"} {
		_, err := Parse(q)
		var got *sqlstate.Error
		if !errors.As(err, &got) || got.Code != "0A000" {
			t.Fatal(q, err)
		}
	}
	for _, q := range []string{"CREATE TABLE t(v TIME WITH TIME ZONE)", "CREATE TABLE t(v TIME(3) WITH TIME ZONE)"} {
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(q, err)
		}
		if got := statements[0].(*CreateTable).Elements[0].Column.TypeName; got.Name != "timetz" {
			t.Fatal(got)
		}
	}
	if _, err := Parse("SELECT TIME '24:00'::time FROM t"); err != nil {
		t.Fatal("cast target scope belongs to binding", err)
	}
	statements, err := Parse("INSERT INTO t VALUES(TIME '24:00'); SELECT TIME '00:00' FROM t junk bad")
	if err == nil || statements != nil {
		t.Fatal("valid prefix escaped", statements, err)
	}
}
