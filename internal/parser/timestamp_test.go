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

func TestTimestampDeclarationsAndTypedLiteralPositions(t *testing.T) {
	for _, tc := range []struct {
		decl string
		p    int32
	}{{"TIMESTAMP", -1}, {"TIMESTAMP WITHOUT TIME ZONE", -1}, {"TIMESTAMP(0)", 0}, {"TIMESTAMP(3) WITHOUT TIME ZONE", 3}, {`"timestamp"(-1)`, -1}, {`"timestamp"(+7)`, 7}} {
		q := "/*é*/ CREATE TABLE timestamp(v " + tc.decl + ")"
		stmts, e := Parse(q)
		if e != nil {
			t.Fatal(q, e)
		}
		raw := stmts[0].(*CreateTable).Elements[0].Column.TypeName
		if raw.Name != "timestamp" || raw.Pos.Character != utf8.RuneCountInString(q[:strings.Index(q, tc.decl)])+1 {
			t.Fatal(raw)
		}
		if strings.Contains(tc.decl, "(") {
			if raw.Precision == nil || *raw.Precision != tc.p {
				t.Fatal(raw)
			}
		} else if raw.Precision != nil {
			t.Fatal("omitted precision lost")
		}
	}
	q := "/*é*/ SELECT TIMESTAMP(3) WITHOUT TIME ZONE '1999-01-08T04:05:06+02',TIMESTAMP 'epoch' FROM timestamp"
	s, e := Parse(q)
	if e != nil {
		t.Fatal(e)
	}
	targets := s[0].(*Select).Targets
	literal, ok := targets[0].(TypedLiteralExpr)
	if !ok || literal.Text != "1999-01-08T04:05:06+02" || literal.TypeName.Precision == nil || *literal.TypeName.Precision != 3 {
		t.Fatal(targets)
	}
	for _, p := range []struct {
		got    Position
		needle string
	}{{literal.TypeName.Pos, "TIMESTAMP"}, {literal.Pos, "'1999"}} {
		want := utf8.RuneCountInString(q[:strings.Index(q, p.needle)]) + 1
		if p.got.Character != want || p.got.Offset != strings.Index(q, p.needle) {
			t.Fatal(p.got, want)
		}
	}
	s, e = Parse("CREATE TABLE timestamp(timestamp TIMESTAMP); INSERT INTO timestamp VALUES(TIMESTAMP 'epoch'); SELECT timestamp FROM timestamp")
	if e != nil || len(s) != 3 {
		t.Fatal(s, e)
	}
	if _, ok = s[2].(*Select).Targets[0].(ColumnExpr); !ok {
		t.Fatal("timestamp keyword lost name eligibility")
	}
}
func TestTimestampPrecisionGrammarAndBoundaries(t *testing.T) {
	for _, tc := range []struct{ decl, token string }{{"TIMESTAMP(-1)", "-"}, {"TIMESTAMP(+1)", "+"}, {"TIMESTAMP()", ")"}, {"TIMESTAMP(1,2)", ","}, {"TIMESTAMP(x)", "x"}, {"TIMESTAMP(1.5)", "1.5"}, {"TIMESTAMP(2147483648)", "2147483648"}} {
		q := "CREATE TABLE bad (v " + tc.decl + ")"
		_, e := Parse(q)
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "syntax error at or near \"" + tc.token + "\"", Position: strconv.Itoa(strings.Index(q, tc.token) + 1)}
		var got *sqlstate.Error
		if !errors.As(e, &got) || !reflect.DeepEqual(*got, want) {
			t.Fatalf("%s got %#v (%v) want %#v", q, got, e, want)
		}
	}
	for _, q := range []string{"CREATE TABLE t(v TIMESTAMP WITH TIME ZONE)", "SELECT TIMESTAMP 'epoch'", "SELECT TIMESTAMP 'epoch'::timestamp FROM t", "CREATE TABLE t(v TIMESTAMPTZ)"} {
		_, e := Parse(q)
		if q == "CREATE TABLE t(v TIMESTAMPTZ)" {
			if e != nil {
				t.Fatal("type authority moved into parser", e)
			}
			continue
		}
		var d *sqlstate.Error
		if !errors.As(e, &d) || d.Code != "0A000" {
			t.Fatal(q, e)
		}
	}
	stmts, e := Parse("INSERT INTO t VALUES(TIMESTAMP 'epoch'); SELECT TIMESTAMP(3) 'epoch' FROM t junk bad")
	if e == nil || stmts != nil {
		t.Fatal("whole script exposed valid prefix", stmts, e)
	}
}
func TestTimestampModifierNumericContextAndIdentifiers(t *testing.T) {
	for _, q := range []string{
		"CREATE TABLE timestamp(timestamp TIMESTAMP(0x3),other INTEGER)",
		"CREATE TABLE t(a TIMESTAMP(0b11),b TIMESTAMP(0o3),c TIMESTAMP(0_3))",
		`CREATE TABLE t(v U&"time\0073tamp"(3))`,
		"SELECT timestamp,other FROM timestamp",
	} {
		if _, err := Parse(q); err != nil {
			t.Fatal(q, err)
		}
	}
	for _, head := range []string{"TIMESTAMP", `"timestamp"`, `U&"time\0073tamp"`} {
		for _, number := range []string{".5", "1e2", "1e+2", "1.5"} {
			q := "/*é*/ CREATE TABLE t(v " + head + "(" + number + "))"
			_, err := Parse(q)
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near "` + number + `"`, Position: strconv.Itoa(len([]rune(q[:strings.Index(q, number)])) + 1)}
			var got *sqlstate.Error
			if !errors.As(err, &got) || *got != want {
				t.Fatal(q, got, err, want)
			}
		}
	}
	for _, q := range []string{"SELECT 1.5 FROM t", "SELECT timestamp,1e2 FROM t", "CREATE TABLE t(v TIMESTAMP(3)); SELECT .5 FROM t"} {
		stmts, err := Parse(q)
		var d *sqlstate.Error
		if stmts != nil || !errors.As(err, &d) || d.Code != "0A000" {
			t.Fatal(q, stmts, err)
		}
	}
}
func TestTimestampKeywordCategoriesAndExcludedSyntaxLocations(t *testing.T) {
	for _, q := range []string{
		"CREATE TABLE timestamp(without TIMESTAMP,time TIMESTAMP(3),zone TIMESTAMP,timestamp TIMESTAMP)",
		"SELECT without,time,zone,timestamp FROM timestamp",
		"SELECT timestamp time FROM t", "SELECT timestamp zone FROM t",
	} {
		_, err := Parse(q)
		if strings.Contains(q, " FROM t") && !strings.Contains(q, "FROM timestamp") {
			var d *sqlstate.Error
			if !errors.As(err, &d) || d.Code != "0A000" {
				t.Fatal(q, err)
			}
		} else if err != nil {
			t.Fatal(q, err)
		}
	}
	for _, word := range []string{"timestamp", "without", "time", "zone"} {
		if _, err := Parse("SET application_name=" + word); err != nil {
			t.Fatal(word, err)
		}
	}
	for _, q := range []string{
		`CREATE TABLE t(v numeric(8))`, `CREATE TABLE t(v "text"(3))`,
		`CREATE TABLE t(v "text"(nonsense))`, `CREATE TABLE t(v "text"(1.5))`,
	} {
		_, err := Parse(q)
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "type modifiers are unsupported", Position: strconv.Itoa(strings.LastIndex(q, "(") + 1)}
		var got *sqlstate.Error
		if !errors.As(err, &got) || *got != want {
			t.Fatal(q, got, err, want)
		}
	}
	for _, q := range []string{"SELECT TIMESTAMP 'epoch'::timestamp FROM t", "INSERT INTO t VALUES(TIMESTAMP(3) 'epoch'::timestamp)", "SELECT * FROM t WHERE v=TIMESTAMP 'epoch'::timestamp"} {
		_, err := Parse(q)
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "casts are unsupported", Position: strconv.Itoa(strings.Index(q, "::") + 1)}
		var got *sqlstate.Error
		if !errors.As(err, &got) || *got != want {
			t.Fatal(q, got, err, want)
		}
	}
}
