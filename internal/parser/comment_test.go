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
		{`COMMENT ON VIEW "s.dot"."view.dot" IS 'retained'`, CommentView, 2, "retained", false},
		{`COMMENT ON VIEW v IS NULL`, CommentView, 1, "", true},
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
	_, databaseErr := Parse("COMMENT ON DATABASE target IS 'x'")
	var databaseDiagnostic *sqlstate.Error
	if !errors.As(databaseErr, &databaseDiagnostic) || databaseDiagnostic.Code != "0A000" {
		t.Fatal("COMMENT ON DATABASE", databaseErr)
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

func TestCommentObjectKindGrammar(t *testing.T) {
	for _, tc := range []struct {
		query string
		check func(*testing.T, *Comment)
	}{
		{`COMMENT ON TYPE t IS 'x'`, func(t *testing.T, c *Comment) {
			if c.Kind != CommentType || c.Type.Name != "t" || c.Text != "x" || c.Null {
				t.Fatal("type", c.Kind, c.Type, c.Text, c.Null)
			}
		}},
		{`COMMENT ON TYPE public.t IS NULL`, func(t *testing.T, c *Comment) {
			if c.Kind != CommentType || len(c.Type.Parts) != 2 || c.Type.Parts[0].Text != "public" || c.Type.Parts[1].Text != "t" || !c.Null {
				t.Fatal("qualified type", c.Kind, c.Type, c.Null)
			}
		}},
		{`COMMENT ON TYPE varchar(10) IS 'x'`, func(t *testing.T, c *Comment) {
			if c.Kind != CommentType || c.Type.Name != "varchar" || len(c.Type.Modifiers) != 1 || c.Text != "x" {
				t.Fatal("modifier type", c.Kind, c.Type, c.Text)
			}
		}},
		{`COMMENT ON TYPE int[] IS 'x'`, func(t *testing.T, c *Comment) {
			if c.Kind != CommentType || c.Type.Name != "integer" || len(c.Type.ArrayBounds) != 1 {
				t.Fatal("array type", c.Kind, c.Type)
			}
		}},
		{`COMMENT ON DOMAIN d IS 'x'`, func(t *testing.T, c *Comment) {
			if c.Kind != CommentDomain || c.Type.Name != "d" || c.Text != "x" {
				t.Fatal("domain", c.Kind, c.Type, c.Text)
			}
		}},
		{`COMMENT ON INDEX "s.dot"."i.dot" IS 'x'`, func(t *testing.T, c *Comment) {
			if c.Kind != CommentIndex || len(c.Name.Parts) != 2 || c.Name.Parts[0].Text != "s.dot" || c.Name.Parts[1].Text != "i.dot" {
				t.Fatal("index", c.Kind, c.Name)
			}
		}},
		{`COMMENT ON TRIGGER trg ON tbl IS 'x'`, func(t *testing.T, c *Comment) {
			if c.Kind != CommentTrigger || len(c.Name.Parts) != 1 || c.Name.Parts[0].Text != "trg" || len(c.Table.Parts) != 1 || c.Table.Parts[0].Text != "tbl" {
				t.Fatal("trigger", c.Kind, c.Name, c.Table)
			}
		}},
		{`COMMENT ON TRIGGER "T" ON "s"."t" IS NULL`, func(t *testing.T, c *Comment) {
			if c.Kind != CommentTrigger || c.Name.Parts[0].Text != "T" || len(c.Table.Parts) != 2 || c.Table.Parts[0].Text != "s" || c.Table.Parts[1].Text != "t" || !c.Null {
				t.Fatal("qualified trigger", c.Kind, c.Name, c.Table, c.Null)
			}
		}},
		{`COMMENT ON FUNCTION f(int) IS 'x'`, func(t *testing.T, c *Comment) {
			if c.Kind != CommentFunction || !c.Routine.SignatureSpecified || len(c.Routine.Arguments) != 1 || c.Routine.Arguments[0].Type.Name != "integer" {
				t.Fatal("function signature", c.Kind, c.Routine)
			}
		}},
		{`COMMENT ON FUNCTION f IS 'x'`, func(t *testing.T, c *Comment) {
			if c.Kind != CommentFunction || c.Routine.SignatureSpecified || len(c.Routine.Name.Parts) != 1 || c.Routine.Name.Parts[0].Text != "f" {
				t.Fatal("bare function", c.Kind, c.Routine)
			}
		}},
		{`COMMENT ON PROCEDURE p(IN a int, OUT b text) IS 'x'`, func(t *testing.T, c *Comment) {
			if c.Kind != CommentProcedure || len(c.Routine.Arguments) != 2 || c.Routine.Arguments[0].Mode != "in" || c.Routine.Arguments[0].Name.Text != "a" || c.Routine.Arguments[0].Type.Name != "integer" || c.Routine.Arguments[1].Mode != "out" || c.Routine.Arguments[1].Name.Text != "b" || c.Routine.Arguments[1].Type.Name != "text" {
				t.Fatal("procedure signature", c.Kind, c.Routine)
			}
		}},
		{`COMMENT ON ROUTINE r(int, text) IS 'x'`, func(t *testing.T, c *Comment) {
			if c.Kind != CommentRoutine || len(c.Routine.Arguments) != 2 || c.Routine.Arguments[0].Type.Name != "integer" || c.Routine.Arguments[1].Type.Name != "text" {
				t.Fatal("routine signature", c.Kind, c.Routine)
			}
		}},
	} {
		stmts, err := Parse(tc.query)
		if err != nil || len(stmts) != 1 {
			t.Fatal(tc.query, stmts, err)
		}
		c, ok := stmts[0].(*Comment)
		if !ok || c.Pos.Offset != 0 {
			t.Fatalf("%s: %#v", tc.query, stmts[0])
		}
		tc.check(t, c)
	}
	for _, q := range []string{
		`COMMENT ON TYPE t IS $1`,
		`COMMENT ON TYPE t IS 7`,
		`COMMENT ON INDEX i IS 'x' extra`,
		`COMMENT ON TRIGGER trg IS 'x'`,
		`COMMENT ON FUNCTION f( IS 'x'`,
		`COMMENT ON TYPE t IS 'x' extra`,
	} {
		stmts, err := Parse("CREATE TABLE prefix(v INT);" + q)
		var diagnostic *sqlstate.Error
		if len(stmts) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Position == "" {
			t.Fatalf("%s: %v %#v", q, stmts, err)
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

func TestCommentTextSearchDictionaryGrammar(t *testing.T) {
	for _, tc := range []struct {
		query string
		parts int
		text  string
		null  bool
	}{
		{`COMMENT ON TEXT SEARCH DICTIONARY "s.dot"."dict.dot" IS E'quote\' slash\\\n雪'`, 2, "quote' slash\\\n雪", false},
		{`COMMENT ON TEXT SEARCH DICTIONARY xsyn IS NULL`, 1, "", true},
		{`COMMENT ON TEXT SEARCH DICTIONARY xsyn IS ''`, 1, "", false},
	} {
		statements, err := Parse(tc.query)
		if err != nil || len(statements) != 1 {
			t.Fatal(tc.query, statements, err)
		}
		c, ok := statements[0].(*Comment)
		if !ok || c.Kind != CommentTextSearchDictionary || len(c.Name.Parts) != tc.parts || c.Text != tc.text || c.Null != tc.null || c.Pos.Offset != 0 {
			t.Fatal("dictionary comment syntax/position", tc, c)
		}
	}
	for _, suffix := range []string{"IS $1", "IS 7", "IS 'x'||'y'", "IS 'x' extra", "IS"} {
		statements, err := Parse("CREATE TABLE prefix(v INT); COMMENT ON TEXT SEARCH DICTIONARY public.xsyn " + suffix)
		var diagnostic *sqlstate.Error
		if len(statements) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Position == "" {
			t.Fatal("invalid dictionary comment accepted a prefix", suffix, statements, err)
		}
	}
}
