package parser

import "testing"

func TestMaterializedStorageGrammar(t *testing.T) {
	for _, tc := range []struct {
		suffix string
		kind   AlterTableActionKind
	}{
		{"ALTER COLUMN id SET STATISTICS 7", SetColumnStatistics},
		{"ALTER id SET STATISTICS DEFAULT", SetColumnStatistics},
		{"ALTER id SET (n_distinct=2,n_distinct_inherited=-0.5)", SetColumnOptions},
		{"ALTER id RESET (n_distinct)", ResetColumnOptions},
		{"ALTER note SET STORAGE EXTERNAL", SetStorage},
		{"ALTER note SET COMPRESSION pglz", SetCompression},
		{"SET (fillfactor=70)", SetRelationOptions},
		{"RESET (fillfactor)", ResetRelationOptions},
		{"CLUSTER ON idx", ClusterOn},
		{"SET WITHOUT CLUSTER", SetWithoutCluster},
		{"SET ACCESS METHOD DEFAULT", SetAccessMethod},
		{"SET TABLESPACE pg_default", SetTablespace},
	} {
		t.Run(tc.suffix, func(t *testing.T) {
			statements, err := Parse(`ALTER MATERIALIZED VIEW IF EXISTS "雪"."Mv" ` + tc.suffix)
			if err != nil {
				t.Fatal(err)
			}
			s, ok := statements[0].(*AlterTable)
			if !ok || !s.Materialized || !s.IfExists || s.Only || s.Star || len(s.Name.Parts) != 2 || s.Name.Parts[0].Text != "雪" || s.Name.Parts[1].Text != "Mv" || len(s.Actions) != 1 || s.Actions[0].Action != tc.kind || s.Actions[0].Pos.Character == 0 {
				t.Fatalf("lost bounded materialized action: %#v", s)
			}
		})
	}
	statements, err := Parse("ALTER MATERIALIZED VIEW mv ALTER id SET STATISTICS 7, SET (fillfactor=70), CLUSTER ON idx, SET TABLESPACE pg_default")
	if err != nil || len(statements[0].(*AlterTable).Actions) != 4 {
		t.Fatal(statements, err)
	}
	for _, spelling := range []string{"TABLE", "MATERIALIZED VIEW"} {
		statements, err := Parse(`ALTER ` + spelling + ` ALL IN TABLESPACE "Source" OWNED BY CURRENT_USER,CURRENT_ROLE,SESSION_USER,"current_user",bob SET TABLESPACE "Destination" NOWAIT`)
		if err != nil {
			t.Fatal(err)
		}
		s := statements[0].(*AlterTableAllInTablespace)
		if s.Materialized != (spelling == "MATERIALIZED VIEW") {
			t.Fatalf("lost bulk target kind for %s: %#v", spelling, s)
		}
		if s.Source.Text != "Source" || s.Source.Pos.Character == 0 || s.Destination.Text != "Destination" || s.Destination.Pos.Character == 0 || !s.Nowait || len(s.Owners) != 5 || s.Owners[0].Identity != "current" || s.Owners[1].Identity != "current" || s.Owners[2].Identity != "session" || s.Owners[3].Identity != "" || s.Owners[3].Name != "current_user" || s.Owners[4].Name != "bob" {
			t.Fatalf("lost standalone utility/roles: %#v", s)
		}
	}
	for _, q := range []string{
		"ALTER MATERIALIZED VIEW mv ADD COLUMN extra int",
		"ALTER MATERIALIZED VIEW mv DROP COLUMN id",
		"ALTER MATERIALIZED VIEW mv ALTER id TYPE bigint",
		"ALTER MATERIALIZED VIEW mv SET LOGGED",
		"ALTER MATERIALIZED VIEW mv SET WITHOUT OIDS",
		"ALTER MATERIALIZED VIEW mv REPLICA IDENTITY FULL",
		"ALTER MATERIALIZED VIEW ONLY mv SET (fillfactor=70)",
		"ALTER MATERIALIZED VIEW mv SET TABLESPACE pg_default NOWAIT",
		"ALTER MATERIALIZED VIEW ALL IN TABLESPACE pg_default SET TABLESPACE pg_default, SET (fillfactor=70)",
		"ALTER MATERIALIZED VIEW IF EXISTS ALL IN TABLESPACE pg_default SET TABLESPACE pg_default",
		"ALTER MATERIALIZED VIEW mv SET (fillfactor=70) trailing",
		"ALTER MATERIALIZED VIEW mv ALTER id SET STATISTICS 7; SELECT FROM",
	} {
		if _, err := Parse(q); err == nil {
			t.Fatal("accepted out-of-scope or incomplete syntax", q)
		}
	}
	// Preserve the pre-existing DEPENDS ambiguity regression while widening
	// only the selected shared action list.
	for _, suffix := range []string{"DEPENDS ON EXTENSION unaccent", "NO DEPENDS ON EXTENSION unaccent"} {
		if _, err := Parse("ALTER MATERIALIZED VIEW mv " + suffix); err != nil {
			t.Fatal(suffix, err)
		}
	}
}
