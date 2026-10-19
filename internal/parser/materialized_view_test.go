package parser

import (
	"errors"
	"reflect"
	"testing"
)

func TestMaterializedViewGrammar(t *testing.T) {
	for _, text := range []string{
		`CREATE MATERIALIZED VIEW "s"."v" ("renamed") AS SELECT 1 AS i`,
		`CREATE MATERIALIZED VIEW IF NOT EXISTS public.v AS VALUES (1),(2) WITH DATA`,
		`CREATE MATERIALIZED VIEW public.v AS SELECT 1/0 AS i WITH NO DATA`,
	} {
		statements, err := Parse(text)
		if err != nil {
			t.Fatal(text, err)
		}
		creation, ok := statements[0].(*CreateQueryTable)
		if !ok || creation.Form != CreateMaterializedView || creation.Query == nil {
			t.Fatalf("not common query-table AST: %#v", statements[0])
		}
		if creation.NoData != (text == `CREATE MATERIALIZED VIEW public.v AS SELECT 1/0 AS i WITH NO DATA`) {
			t.Fatal("lost population clause")
		}
	}
	statements, err := Parse(`DROP MATERIALIZED VIEW IF EXISTS public.v, "s"."v2" CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
	drop, ok := statements[0].(*DropView)
	if !ok || !drop.Materialized || !drop.IfExists || drop.Mode != DropCascade || len(drop.Names) != 2 {
		t.Fatalf("not grouped-kind drop: %#v", statements[0])
	}
	for _, text := range []string{
		`CREATE OR REPLACE MATERIALIZED VIEW v AS SELECT 1`,
		`CREATE TEMP MATERIALIZED VIEW v AS SELECT 1`,
		`CREATE MATERIALIZED VIEW v AS SELECT 1 INTO other`,
	} {
		if _, err := Parse(text); err == nil {
			t.Fatalf("accepted excluded syntax %s", text)
		}
	}
	if _, err := Parse(`SELECT materialized AS materialized`); err != nil {
		t.Fatal("contextual keyword lost identifier compatibility:", err)
	}
}

func TestMaterializedViewRestoreComments(t *testing.T) {
	text := "SELECT '-- stuntgres-matview-v1 quoted'::text; /* outer -- stuntgres-matview-v1 block /* nested */ */\n-- stuntgres-matview-v1 actual\nSELECT 2; -- ordinary\n"
	var comments []string
	statements, err := ParseWithOptions(text, Options{StandardConformingStrings: true, LineComment: func(comment string, _ Position) error { comments = append(comments, comment); return nil }})
	if err != nil || len(statements) != 2 {
		t.Fatalf("opt-in comment parsing: %v", err)
	}
	if !reflect.DeepEqual(comments, []string{" stuntgres-matview-v1 actual", " ordinary"}) {
		t.Fatalf("quoted/block text gained authority: %#v", comments)
	}
	if statements, err := Parse(text); err != nil || len(statements) != 2 {
		t.Fatalf("ordinary SQL comments changed behavior: %v", err)
	}
	failure := errors.New("bad private snapshot")
	if _, err := ParseWithOptions("-- stuntgres-matview-v1 bad\nSELECT 1", Options{LineComment: func(string, Position) error { return failure }}); !errors.Is(err, failure) {
		t.Fatalf("callback error lost: %v", err)
	}
}

func TestLineCommentPositions(t *testing.T) {
	type comment struct {
		text string
		pos  Position
	}
	var got []comment
	statements, err := ParseWithOptions("-- é\n-- 二\n-- z\nSELECT 1", Options{LineComment: func(text string, pos Position) error {
		got = append(got, comment{text, pos})
		// Reentry must not alter the outer callback's byte/character frontier.
		_, err := Parse("-- nested\nSELECT 2")
		return err
	}})
	want := []comment{{" é", Position{Offset: 2, Character: 3}}, {" 二", Position{Offset: 8, Character: 8}}, {" z", Position{Offset: 15, Character: 13}}}
	if err != nil || len(statements) != 1 || !reflect.DeepEqual(got, want) {
		t.Fatalf("positioned comments: %#v, %v", got, err)
	}
}
