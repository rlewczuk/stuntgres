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
	}{{"TIMESTAMP", -1}, {"TIMESTAMP WITHOUT TIME ZONE", -1}, {"TIMESTAMP(0)", 0}, {"TIMESTAMP(3) WITHOUT TIME ZONE", 3}, {`"timestamp"(-1)`, -1}, {`"timestamp"(7)`, 7}} {
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
			if len(raw.Modifiers) != 1 || raw.Modifiers[0].Integer == nil || *raw.Modifiers[0].Integer != tc.p {
				t.Fatal(raw)
			}
		} else if len(raw.Modifiers) != 0 {
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
	if !ok || literal.Text != "1999-01-08T04:05:06+02" || len(literal.TypeName.Modifiers) != 1 || literal.TypeName.Modifiers[0].Integer == nil || *literal.TypeName.Modifiers[0].Integer != 3 {
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
	for _, q := range []string{"CREATE TABLE t(v TIMESTAMP WITH TIME ZONE)", "SELECT TIMESTAMP 'epoch'", "CREATE TABLE t(v TIMESTAMPTZ)"} {
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
			if head != "TIMESTAMP" {
				stmts, err := Parse(q)
				if err != nil {
					t.Fatal(q, err)
				}
				raw := stmts[0].(*CreateTable).Elements[0].Column.TypeName
				if raw.Modifiers[0].Raw != number || raw.Modifiers[0].Integer != nil {
					t.Fatal(raw, number)
				}
				continue
			}
			_, err := Parse(q)
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near "` + number + `"`, Position: strconv.Itoa(len([]rune(q[:strings.Index(q, number)])) + 1)}
			var got *sqlstate.Error
			if !errors.As(err, &got) || *got != want {
				t.Fatal(q, got, err, want)
			}
		}
	}
	for _, tc := range []struct{ q, text string }{
		{"SELECT 1.5 FROM t", "1.5"},
		{"SELECT timestamp,1e2 FROM t", "1e2"},
		{"CREATE TABLE t(v TIMESTAMP(3)); SELECT .5 FROM t", ".5"},
	} {
		stmts, err := Parse(tc.q)
		if err != nil || len(stmts) == 0 {
			t.Fatal(tc.q, stmts, err)
		}
		targets := stmts[len(stmts)-1].(*Select).Targets
		literal, ok := targets[len(targets)-1].(LiteralExpr)
		if !ok || literal.Value.Kind != NumericValue || literal.Value.Text != tc.text {
			t.Fatal(tc.q, targets)
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
	for _, q := range []string{"SELECT TIMESTAMP 'epoch'::timestamp FROM t", "INSERT INTO t VALUES(TIMESTAMP(3) 'epoch'::timestamp)", "SELECT * FROM t WHERE v=TIMESTAMP 'epoch'::timestamp"} {
		if _, err := Parse(q); err != nil {
			t.Fatal(q, err) // Cast scope is a binder policy, not parser rejection.
		}
	}
}

// Expectations are release scanner outcomes, not tokens recovered from Parse.
func TestTimestampPrecisionScannerPrecedence(t *testing.T) {
	for _, head := range []struct {
		sql    string
		create int
	}{
		{"TIMESTAMP", 30},
		{`"timestamp"`, 32},
		{`U&"t\0069mestamp"`, 38},
	} {
		for _, tc := range []struct {
			spelling, token, primary string
			delta                    int
		}{
			{"1.5foo", "1.5foo", "trailing junk after numeric literal", 0},
			{".5foo", ".5foo", "trailing junk after numeric literal", 0},
			{"1.5e+", "1.5e+", "trailing junk after numeric literal", 0},
			{"1..2", "..", "syntax error", 1},
			{".5e-", ".5e-", "trailing junk after numeric literal", 0},
			{"1.5e", "1.5e", "trailing junk after numeric literal", 0},
			{"1.5e+3foo", "1.5e+3foo", "trailing junk after numeric literal", 0},
			{"1.5_", "1.5_", "trailing junk after numeric literal", 0},
			{"1.5e+3_", "1.5e+3_", "trailing junk after numeric literal", 0},
			{".5", ".5", "syntax error", 0},
			{"1.5e+3", "1.5e+3", "syntax error", 0},
			{"1e+2", "1e+2", "syntax error", 0},
			{"1e+", "1e+", "trailing junk after numeric literal", 0},
			{"1efoo", "1efoo", "trailing junk after numeric literal", 0},
			{"1.5e3foo", "1.5e3foo", "trailing junk after numeric literal", 0},
			{"1.", "1.", "syntax error", 0},
		} {
			if head.sql != "TIMESTAMP" && tc.primary == "syntax error" && tc.delta == 0 {
				for _, table := range []string{"bad", `"é"`} {
					q := "CREATE TABLE " + table + "(v " + head.sql + "(" + tc.spelling + "))"
					stmts, err := Parse(q)
					if err != nil {
						t.Fatal(q, err)
					}
					raw := stmts[0].(*CreateTable).Elements[0].Column.TypeName
					if raw.Modifiers[0].Raw != tc.spelling || raw.Modifiers[0].Integer != nil || raw.Pos.Character != 20 {
						t.Fatal(q, raw)
					}
				}
				continue
			}
			queries := []struct {
				sql      string
				position int
			}{
				{"CREATE TABLE bad(v " + head.sql + "(" + tc.spelling + "))", head.create},
				{`CREATE TABLE "é"(v ` + head.sql + "(" + tc.spelling + "))", head.create},
			}
			if head.sql == "TIMESTAMP" {
				queries = append(queries, struct {
					sql      string
					position int
				}{`INSERT INTO "é" VALUES(TIMESTAMP 'epoch'),(TIMESTAMP(` + tc.spelling + ") 'epoch')", 54})
			}
			for _, query := range queries {
				t.Run(query.sql, func(t *testing.T) {
					stmts, err := Parse(query.sql)
					var got *sqlstate.Error
					want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: tc.primary + ` at or near "` + tc.token + `"`, Position: strconv.Itoa(query.position + tc.delta)}
					if stmts != nil || !errors.As(err, &got) || *got != want {
						t.Fatalf("statements=%#v diagnostic=%#v (%v), want %#v", stmts, got, err, want)
					}
				})
			}
		}
	}
}

func TestTimestampPrecisionScannerGrammarIsolation(t *testing.T) {
	for _, head := range []string{`"timestamp"`, `U&"t\0069mestamp"`} {
		for _, precision := range []string{"+3", "-1"} {
			stmts, err := Parse("CREATE TABLE t(v " + head + "(" + precision + "))")
			if err != nil {
				t.Fatal(err)
			}
			want := int32(3)
			if precision == "-1" {
				want = -1
			}
			raw := stmts[0].(*CreateTable).Elements[0].Column.TypeName
			got := raw.Modifiers[0].Integer
			if got == nil || *got != want || raw.Modifiers[0].UnaryPlus != (precision == "+3") {
				t.Fatal(raw, want)
			}
		}
	}
	for _, tc := range []struct{ query, code, message, position string }{
		{"CREATE TABLE bad(v TIMESTAMP(+3))", "42601", `syntax error at or near "+"`, "30"},
		{"CREATE TABLE bad(v TIMESTAMP(-1))", "42601", `syntax error at or near "-"`, "30"},
		{`CREATE TABLE bad(v "timestamp"(+1.5foo))`, "42601", `trailing junk after numeric literal at or near "1.5foo"`, "33"},
		{`CREATE TABLE bad(v "timestamp"(-1..2))`, "42601", `syntax error at or near ".."`, "34"},
		{`CREATE TABLE bad(v U&"t\0069mestamp"(+1.5foo))`, "42601", `trailing junk after numeric literal at or near "1.5foo"`, "39"},
		{`CREATE TABLE bad(v U&"t\0069mestamp"(-1..2))`, "42601", `syntax error at or near ".."`, "40"},
		{"CREATE TABLE bad(v FLOAT(+3))", "42601", `syntax error at or near "+"`, "26"},
		{"CREATE TABLE bad(v FLOAT(1.5foo))", "42601", `trailing junk after numeric literal at or near "1.5foo"`, "26"},
		{"CREATE TABLE bad(v FLOAT(1..2))", "42601", `syntax error at or near ".."`, "27"},
	} {
		stmts, err := Parse(tc.query)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.position}
		if stmts != nil || !errors.As(err, &got) || *got != want {
			t.Fatal(tc.query, stmts, got, err, want)
		}
	}
}
