package parser

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestTableCoreOrderedSyntax(t *testing.T) {
	statements, err := Parse(`CREATE TABLE z(); CREATE TABLE t(head BOOL,LIKE public.one,middle BIGINT,LIKE "two.dot",tail TEXT); DROP TABLE IF EXISTS t,public.z,t CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
	if len(statements) != 3 || len(statements[0].(*CreateTable).Elements) != 0 {
		t.Fatal(statements)
	}
	c := statements[1].(*CreateTable)
	if len(c.Elements) != 5 || c.Elements[0].Column.Name != "head" || c.Elements[1].Like.Name.Parts[1].Text != "one" || c.Elements[2].Column.Name != "middle" || c.Elements[3].Like.Name.Parts[0].Text != "two.dot" || c.Elements[4].Column.Name != "tail" {
		t.Fatal(c)
	}
	d := statements[2].(*DropTable)
	if !d.IfExists || d.Mode != DropCascade || len(d.Names) != 3 || d.Names[1].Parts[0].Text != "public" {
		t.Fatal(d)
	}
	for _, q := range []string{"DROP TABLE t", "DROP TABLE t RESTRICT"} {
		s, err := Parse(q)
		if err != nil || s[0].(*DropTable).Mode != DropRestrict {
			t.Fatal(s, err)
		}
	}
	s, err := Parse("/*é*/ CREATE TABLE bad(id no_such_type)")
	if err != nil {
		t.Fatal(err)
	}
	// Six prefix characters, then "CREATE TABLE bad(id " (20): type starts at 27.
	if got := s[0].(*CreateTable).Elements[0].Column.TypeName; got.Name != "no_such_type" || got.Pos != (Position{Offset: 27, Character: 27}) {
		t.Fatal(got)
	}
}
func TestTableCoreOptionsAndBoundaries(t *testing.T) {
	for _, option := range []string{"COMMENTS", "COMPRESSION", "CONSTRAINTS", "DEFAULTS", "GENERATED", "IDENTITY", "INDEXES", "STATISTICS", "STORAGE", "ALL"} {
		for _, prefix := range []string{"INCLUDING", "EXCLUDING"} {
			s, err := Parse("CREATE TABLE t(LIKE source " + prefix + " " + option + ")")
			if err != nil {
				t.Fatal(err)
			}
			got := s[0].(*CreateTable).Elements[0].Like.Options[0]
			// "CREATE TABLE t(LIKE source " is 27 ASCII characters.
			if got.Including != (prefix == "INCLUDING") || got.Pos != (Position{Offset: 27, Character: 28}) {
				t.Fatal(got)
			}
		}
	}
	for _, q := range []string{"CREATE TABLE t(LIKE s INCLUDING nonsense)", `CREATE TABLE t(LIKE s INCLUDING "comments")`, "CREATE TABLE t(,)", "CREATE TABLE t(LIKE)", "DROP TABLE t,", "DROP TABLE t CASCADE RESTRICT", "DROP TABLE t; nonsense", "INSERT INTO z VALUES ()"} {
		_, err := Parse(q)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "42601" {
			t.Fatalf("%s: %v", q, err)
		}
	}
	s, err := Parse("CREATE TABLE cascade(restrict INT,including TEXT,excluding BOOL); SELECT like FROM cascade")
	if err == nil {
		t.Fatal("LIKE is not an ordinary column name", s)
	}
	s, err = Parse("CREATE TABLE cascade(restrict INT,including TEXT,excluding BOOL)")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range s[0].(*CreateTable).Elements {
		names = append(names, e.Column.Name)
	}
	if !reflect.DeepEqual(names, []string{"restrict", "including", "excluding"}) {
		t.Fatal(names)
	}
}

func TestTableCoreExcludedTypeSyntaxNeverSkips(t *testing.T) {
	for _, declaration := range []string{"CHARACTER VARYING", "BIT VARYING", "TIME WITH TIME ZONE", "TIMESTAMP WITH TIME ZONE", "INTERVAL YEAR TO MONTH", "public.int4", "INT(4)", "INT[]"} {
		_, err := Parse("CREATE TABLE IF NOT EXISTS t (id " + declaration + ")")
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" {
			t.Fatal(declaration, err)
		}
	}
	// The family guards must not reinterpret arbitrary adjacent type/name tokens.
	_, err := Parse("CREATE TABLE t(id arbitrary extra)")
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
		t.Fatal(err)
	}
}

func TestTableCoreQuotedTypeSyntaxProvenance(t *testing.T) {
	statements, err := Parse(`CREATE TABLE t(a "integer",b "int4",c U&"in\0074\0034",d INTEGER,e INT4)`)
	if err != nil {
		t.Fatal(err)
	}
	elements := statements[0].(*CreateTable).Elements
	for i, want := range []struct {
		name   string
		quoted bool
	}{{"integer", true}, {"int4", true}, {"int4", true}, {"integer", false}, {"integer", false}} {
		got := elements[i].Column.TypeName
		if got.Name != want.name || got.Quoted != want.quoted {
			t.Fatal(i, got, want)
		}
	}
}

func TestTableCoreTypeTailSourceLocations(t *testing.T) {
	// The type begins at character 19; an arbitrary nine-character type's
	// following token begins at 29. Exercise each suffix/type-tail overlap.
	for _, tc := range []struct{ declaration, code, position string }{
		{"TIME WITH TIME ZONE", "0A000", "19"},
		{"arbitrary ISNULL", "42601", "29"},
		{"arbitrary LIKE", "42601", "29"},
		{"arbitrary WITH", "42601", "29"},
	} {
		_, err := Parse("CREATE TABLE t(id " + tc.declaration + ")")
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Position != tc.position {
			t.Fatal(tc.declaration, err)
		}
	}
}

func TestTableCoreFloatTimestampModifierCoexistence(t *testing.T) {
	q := `/*é*/ CREATE TABLE t(a FLOAT(24),b U&"time\0073tamp"(-1),c FLOAT(25),d TIMESTAMP(3) WITHOUT TIME ZONE,e "timestamp"(7),f DOUBLE PRECISION)`
	statements, err := Parse(q)
	if err != nil {
		t.Fatal(err)
	}
	elements := statements[0].(*CreateTable).Elements
	for i, want := range []struct {
		name      string
		precision *int32
		quoted    bool
	}{{"float4", nil, false}, {"timestamp", new(int32(-1)), true}, {"float8", nil, false}, {"timestamp", new(int32(3)), false}, {"timestamp", new(int32(7)), true}, {"double precision", nil, false}} {
		got := elements[i].Column.TypeName
		var precision *int32
		if len(got.Modifiers) > 0 {
			precision = got.Modifiers[0].Integer
		}
		if got.Name != want.name || got.Quoted != want.quoted || !reflect.DeepEqual(precision, want.precision) {
			t.Fatal(i, got, want)
		}
	}
	for _, tc := range []struct{ declaration, token string }{
		{"FLOAT(+1)", "+"}, {"FLOAT(-1)", "-"}, {"FLOAT(1.5)", "1.5"},
	} {
		q := `/*é*/ CREATE TABLE t(a "timestamp"(-1),b ` + tc.declaration + `)`
		_, err := Parse(q)
		var got *sqlstate.Error
		offset := strings.LastIndex(q, tc.token)
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near "` + tc.token + `"`, Position: strconv.Itoa(len([]rune(q[:offset])) + 1)}
		if !errors.As(err, &got) || *got != want {
			t.Fatal(q, got, err, want)
		}
	}
	// A complete numeric token in a quoted generic modifier is retained for
	// descriptor-time int4 input, unlike FLOAT's unsigned Iconst grammar.
	generic, err := Parse(`/*é*/ CREATE TABLE t(a "timestamp"(-1),b U&"time\0073tamp"(1.5))`)
	if err != nil {
		t.Fatal(err)
	}
	raw := generic[0].(*CreateTable).Elements[1].Column.TypeName
	if len(raw.Modifiers) != 1 || raw.Modifiers[0].Raw != "1.5" || raw.Modifiers[0].Integer != nil {
		t.Fatal(raw)
	}
	_, err = Parse(`CREATE TABLE t(a FLOAT(24),b "timestamp"(3)); SELECT 1.5 FROM t`)
	if err != nil {
		t.Fatal("modifier context leaked into scalar input", err)
	}
}
