package parser

import (
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestCommentGrammar(t *testing.T) {
	for _, tc := range []struct {
		query string
		kind  CommentKind
		parts int
		text  string
		null  bool
	}{
		{`COMMENT ON TABLE T IS 'first'`, CommentTable, 1, "first", false},
		{`COMMENT ON SEQUENCE "s.dot"."seq.dot" IS 'generator'`, CommentSequence, 2, "generator", false},
		{`COMMENT ON SEQUENCE seq IS NULL`, CommentSequence, 1, "", true},
		{`COMMENT ON COLUMN "s.dot"."t.dot"."c.dot" IS E'quote\' slash\\\n雪'`, CommentColumn, 3, "quote' slash\\\n雪", false},
		{`COMMENT ON COLUMN c IS NULL`, CommentColumn, 1, "", true},
		{`COMMENT ON SCHEMA public IS $$line
雪$$`, CommentSchema, 1, "line\n雪", false},
		{`COMMENT ON ROLE "PUBLIC" IS ''`, CommentRole, 1, "", false},
		{"COMMENT ON TABLE t IS 'a'\n'b'", CommentTable, 1, "ab", false},
		{`COMMENT ON TABLE t IS U&'d\0061ta'`, CommentTable, 1, "data", false},
	} {
		stmts, err := Parse(tc.query)
		if err != nil {
			t.Fatal(tc.query, err)
		}
		c, ok := stmts[0].(*Comment)
		if !ok || c.Kind != tc.kind || len(c.Name.Parts) != tc.parts || c.Text != tc.text || c.Null != tc.null {
			t.Fatalf("%s: %#v", tc.query, c)
		}
	}
	for _, q := range []string{`COMMENT ON TABLE t IS $1`, `COMMENT ON TABLE t IS 7`, `COMMENT ON TABLE t IS 'x' || 'y'`, `COMMENT ON TABLE IF EXISTS t IS 'x'`, `COMMENT ON TABLE a,b IS 'x'`, `COMMENT ON ROLE s.r IS 'x'`, `COMMENT ON TABLE t IS 'x' extra`} {
		stmts, err := Parse("CREATE TABLE prefix(v INT);" + q)
		var diagnostic *sqlstate.Error
		if len(stmts) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Position == "" {
			t.Fatalf("%s: %v %#v", q, stmts, err)
		}
	}
	for _, kind := range []string{"TYPE", "DATABASE", "INDEX", "VIEW"} {
		_, err := Parse("COMMENT ON " + kind + " target IS 'x'")
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "0A000" {
			t.Fatal(kind, err)
		}
	}
	if _, err := Parse(`CREATE TABLE comment(comment INT); SELECT comment FROM comment`); err != nil {
		t.Fatal(err)
	}
	stmts, err := Parse("COMMENT ON TABLE " + strings.Repeat("A", 64) + " IS 'x'")
	if err != nil || stmts[0].(*Comment).Name.Parts[0].Text != strings.Repeat("a", 63) {
		t.Fatal(stmts, err)
	}
	for _, mode := range []bool{false, true} {
		stmts, err := ParseWithOptions(`COMMENT ON TABLE t IS 'a\nb'`, Options{StandardConformingStrings: mode})
		want := "a\nb"
		if mode {
			want = `a\nb`
		}
		if err != nil || stmts[0].(*Comment).Text != want {
			t.Fatal(mode, stmts, err)
		}
	}
}

func TestCommentExtensionGrammar(t *testing.T) {
	for _, tc := range []struct {
		query, name, text string
		null              bool
	}{
		{`COMMENT ON EXTENSION "Ext.dot" IS 'quote''雪'`, "Ext.dot", "quote'雪", false},
		{`COMMENT ON EXTENSION fuzzystrmatch IS NULL`, "fuzzystrmatch", "", true},
		{`COMMENT ON EXTENSION fuzzystrmatch IS ''`, "fuzzystrmatch", "", false},
		{`COMMENT ON EXTENSION fuzzystrmatch IS E'line\nslash\\'`, "fuzzystrmatch", "line\nslash\\", false},
		{`COMMENT ON EXTENSION fuzzystrmatch IS $$雪$$`, "fuzzystrmatch", "雪", false},
		{`COMMENT ON EXTENSION fuzzystrmatch IS U&'d\0061ta'`, "fuzzystrmatch", "data", false},
	} {
		statements, err := Parse(tc.query)
		if err != nil || len(statements) != 1 {
			t.Fatal(tc.query, statements, err)
		}
		comment, ok := statements[0].(*Comment)
		if !ok || comment.Kind != CommentExtension || len(comment.Name.Parts) != 1 || comment.Name.Parts[0].Text != tc.name || comment.Text != tc.text || comment.Null != tc.null || comment.Pos.Offset != 0 {
			t.Fatalf("%s: %#v", tc.query, comment)
		}
	}
	for _, query := range []string{
		`COMMENT ON EXTENSION public.fuzzystrmatch IS 'x'`,
		`COMMENT ON EXTENSION IF EXISTS fuzzystrmatch IS 'x'`,
		`COMMENT ON EXTENSION fuzzystrmatch,plpgsql IS 'x'`,
		`COMMENT ON EXTENSION fuzzystrmatch IS $1`,
		`COMMENT ON EXTENSION fuzzystrmatch IS 7`,
		`COMMENT ON EXTENSION fuzzystrmatch IS 'x'||'y'`,
		`COMMENT ON EXTENSION fuzzystrmatch 'bad'`,
		`COMMENT ON EXTENSION fuzzystrmatch IS`,
		`COMMENT ON EXTENSION fuzzystrmatch IS 'x' extra`,
	} {
		statements, err := Parse("CREATE TABLE prefix(v INT);" + query)
		var diagnostic *sqlstate.Error
		if len(statements) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Position == "" {
			t.Fatal(query, statements, err)
		}
	}
}
