package parser

import "testing"

func TestMaterializedAlterGrammar(t *testing.T) {
	for _, tc := range []struct {
		suffix, action, target, column string
		ifExists                       bool
	}{
		{"RENAME TO saved", "rename", "saved", "", false},
		{"RENAME i TO key", "column", "key", "i", false},
		{"RENAME COLUMN i TO key", "column", "key", "i", true},
		{"OWNER TO CURRENT_USER", "owner", "current_user", "", true},
		{"SET SCHEMA materialized", "schema", "materialized", "", true},
		{"DEPENDS ON EXTENSION unaccent", "depends", "unaccent", "", false},
		{"NO DEPENDS ON EXTENSION unaccent", "no_depends", "unaccent", "", false},
	} {
		prefix := `/*é*/ ALTER MATERIALIZED VIEW `
		if tc.ifExists {
			prefix += "IF EXISTS "
		}
		q := prefix + `"s"."snow雪" ` + tc.suffix
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		s, ok := statements[0].(*AlterView)
		if !ok || !s.Materialized || s.IfExists != tc.ifExists || s.Action != tc.action || s.Target.Text != tc.target || s.Column.Text != tc.column || s.Pos.Character != 7 || len(s.Name.Parts) != 2 || s.Name.Parts[0].Text != "s" || s.Name.Parts[1].Text != "snow雪" {
			t.Fatalf("lost materialized ALTER AST: %#v", statements[0])
		}
	}
	for _, q := range []string{
		"ALTER MATERIALIZED VIEW IF EXISTS mv DEPENDS ON EXTENSION unaccent",
		"ALTER MATERIALIZED VIEW IF EXISTS mv NO DEPENDS ON EXTENSION unaccent",
		"ALTER MATERIALIZED VIEW ONLY mv RENAME TO saved",
		"ALTER MATERIALIZED VIEW mv RENAME TO $1",
		"ALTER MATERIALIZED VIEW mv RENAME TO saved trailing",
		"ALTER MATERIALIZED VIEW mv RENAME i TO key, RENAME s TO val",
		"ALTER MATERIALIZED VIEW mv SET TABLESPACE pg_default",
		"ALTER MATERIALIZED VIEW mv SET (autovacuum_enabled=false)",
		"ALTER MATERIALIZED VIEW mv ENABLE ROW LEVEL SECURITY",
		"PREPARE p AS ALTER MATERIALIZED VIEW mv RENAME TO saved",
	} {
		if _, err := Parse(q); err == nil {
			t.Fatal("accepted excluded materialized ALTER", q)
		}
	}
	statements, err := Parse("ALTER VIEW ordinary RENAME TO saved; ALTER MATERIALIZED VIEW mv OWNER TO SESSION_USER")
	if err != nil || len(statements) != 2 || statements[0].(*AlterView).Materialized || !statements[1].(*AlterView).Materialized {
		t.Fatal("changed ordinary ALTER or complete-message grammar", statements, err)
	}
	if owner := statements[1].(*AlterView); !owner.OwnerIdentity || owner.Target.Text != "session_user" {
		t.Fatal("lost owner role spec")
	}
	if _, err := Parse("ALTER MATERIALIZED VIEW mv RENAME TO changed; SELECT FROM"); err == nil {
		t.Fatal("accepted malformed complete message")
	}
}
