package parser

import "testing"

func TestMaterializedRefreshGrammar(t *testing.T) {
	for _, tc := range []struct {
		sql, schema, name  string
		noData, concurrent bool
	}{
		{"REFRESH MATERIALIZED VIEW refresh", "", "refresh", false, false},
		{"REFRESH MATERIALIZED VIEW public.materialized WITH DATA", "public", "materialized", false, false},
		{`/*é*/ REFRESH MATERIALIZED VIEW "s"."snow雪" WITH NO DATA`, "s", "snow雪", true, false},
		{"REFRESH MATERIALIZED VIEW CONCURRENTLY mv WITH DATA", "", "mv", false, true},
	} {
		statements, err := Parse(tc.sql)
		if err != nil || len(statements) != 1 {
			t.Fatal(tc.sql, statements, err)
		}
		s, ok := statements[0].(*RefreshMaterializedView)
		if !ok || s.NoData != tc.noData || (s.Concurrent.Character != 0) != tc.concurrent || s.Name.Parts[len(s.Name.Parts)-1].Text != tc.name {
			t.Fatalf("lost refresh AST: %#v", statements[0])
		}
		if tc.schema != "" && (len(s.Name.Parts) != 2 || s.Name.Parts[0].Text != tc.schema) {
			t.Fatal(s.Name)
		}
		if tc.concurrent && s.Concurrent.Character != 27 {
			t.Fatal(s.Concurrent)
		}
		if s.Pos.Character != 1 && s.Pos.Character != 7 {
			t.Fatal(s.Pos)
		}
	}
	for _, sql := range []string{"REFRESH MATERIALIZED VIEW IF EXISTS mv", "REFRESH MATERIALIZED VIEW mv,mv2", "REFRESH MATERIALIZED VIEW mv(1)", "REFRESH MATERIALIZED VIEW mv WITH DATA extra", "REFRESH MATERIALIZED VIEW mv WITH NO", "PREPARE p AS REFRESH MATERIALIZED VIEW mv"} {
		if _, err := Parse(sql); err == nil {
			t.Fatal("accepted malformed refresh", sql)
		}
	}
	for _, sql := range []string{"CREATE TYPE refresh AS ENUM('x')", "CREATE TABLE materialized(i refresh)", "ALTER COLLATION materialized REFRESH VERSION"} {
		if _, err := Parse(sql); err != nil {
			t.Fatal("changed existing keyword grammar", sql, err)
		}
	}
}
