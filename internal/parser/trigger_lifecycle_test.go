package parser

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParseTriggerLifecycle(t *testing.T) {
	for _, tc := range []struct{ sql, action, target string }{
		{`ALTER TRIGGER "名 ' old" ON "s 名"."t 名" RENAME TO "新名"`, "rename", "新名"},
		{`ALTER TRIGGER "名 ' old" ON "s 名"."t 名" DEPENDS ON EXTENSION "ext 名"`, "depends", "ext 名"},
		{`ALTER TRIGGER "名 ' old" ON "s 名"."t 名" NO DEPENDS ON EXTENSION "ext 名"`, "no_depends", "ext 名"},
	} {
		statements, err := Parse(tc.sql)
		if err != nil {
			t.Fatal(err)
		}
		s, ok := statements[0].(*AlterTrigger)
		if len(statements) != 1 || !ok || s.Action != tc.action || s.Name.Text != "名 ' old" || s.Target.Text != tc.target || len(s.Table.Parts) != 2 || s.Table.Parts[0].Text != "s 名" || s.Table.Parts[1].Text != "t 名" {
			t.Fatalf("lost lifecycle AST: %#v", statements)
		}
		byteOffset := strings.Index(tc.sql, `"`+tc.target+`"`)
		if s.Pos.Character != 1 || s.Name.Pos.Character != 15 || s.Target.Pos.Offset != byteOffset || s.Target.Pos.Character != utf8.RuneCountInString(tc.sql[:byteOffset])+1 {
			t.Fatal("lost character/byte positions", s)
		}
	}
	for _, ifExists := range []string{"", "IF EXISTS "} {
		for _, mode := range []string{"", " RESTRICT", " CASCADE"} {
			statements, err := Parse(`DROP TRIGGER ` + ifExists + `"名" ON public.t` + mode)
			if err != nil {
				t.Fatal(err)
			}
			s, ok := statements[0].(*DropTrigger)
			want := DropRestrict
			if mode == " CASCADE" {
				want = DropCascade
			}
			if !ok || s.Name.Text != "名" || s.IfExists != (ifExists != "") || s.Mode != want || s.Pos.Character != 1 {
				t.Fatal("lost drop identity/options", s)
			}
		}
	}
	for _, sql := range []string{
		"ALTER TRIGGER a RENAME TO b", "ALTER TRIGGER a ON t RENAME b", "ALTER TRIGGER a ON t DEPENDS EXTENSION e", "ALTER TRIGGER a ON t NO ON EXTENSION e", "ALTER TRIGGER IF EXISTS a ON t RENAME TO b", "DROP TRIGGER a", "DROP TRIGGER a ON t RESTRICT CASCADE", "ALTER TRIGGER a ON t RENAME TO b garbage", "ALTER TRIGGER a ON t DEPENDS ON EXTENSION e garbage", "DROP TRIGGER a ON t garbage",
	} {
		if _, err := Parse(sql); err == nil {
			t.Fatal("accepted malformed/incomplete-input statement", sql)
		}
	}
}
