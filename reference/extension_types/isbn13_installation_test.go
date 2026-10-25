package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceIsbn13InstallCollisions(t *testing.T) {
	for _, tc := range []struct{ name, setup, kind string }{
		{"scalar", "CREATE TYPE isbn13 AS ENUM ('occupied')", "e"},
		{"domain", "CREATE DOMAIN isbn13 AS text", "d"},
		{"rowtype", "CREATE TABLE isbn13(v integer)", "c"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec(tc.setup, 0))
				run(fixture.Exec("CREATE TABLE isbn13_sentinel(id integer NOT NULL)", 0))
				run(fixture.Exec("INSERT INTO isbn13_sentinel VALUES(7)", 1))
				var original, companion int64
				if err := c.QueryRowContext(p.Ctx, "SELECT oid,typarray FROM pg_type WHERE oid='isbn13'::regtype::oid").Scan(&original, &companion); err != nil {
					t.Fatal(err)
				}
				want := ean13Diagnostic("42710", `type "isbn13" already exists`)
				want.Context = "SQL statement \"CREATE TYPE isbn13 (\n\tINPUT = isbn13_in,\n\tOUTPUT = ean13_out,\n\tLIKE = pg_catalog.int8\n)\"\nextension script file \"isn--1.1.sql\", near line 50"
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("CREATE EXTENSION isn", want)
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
					run(fixture.Select("SELECT oid=$1::oid AS retained,typarray=$2::oid AS companion,typtype FROM pg_type WHERE oid='isbn13'::regtype::oid", []string{"retained", "companion", "typtype"}, []string{"BOOL", "BOOL", "CHAR"}, [][]any{{true, true, tc.kind}}, original, companion))
					isbnInstallUnpublished(t, p, b, c, "")
					run(fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('isbn','_isbn')", []string{"typname"}, []string{"NAME"}, [][]any{}))
					run(fixture.Select("SELECT id FROM isbn13_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
				}
			})
		})
	}
	// A generated _isbn13 companion cannot be independently renamed into this
	// state through public PostgreSQL DDL. Genuine descriptor-unit displacement
	// covers that installer entrance; public occupancy uses real enum/domain
	// scalars rather than pretending an unrelated scalar is an array.
	for _, tc := range []struct{ name, setup, kind string }{
		{"unrelated_array_name", "CREATE TYPE _isbn13 AS ENUM ('kept')", "e"},
		{"unrelated_domain_array_name", "CREATE DOMAIN _isbn13 AS text", "d"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec(tc.setup, 0))
				var prior int64
				if err := c.QueryRowContext(p.Ctx, "SELECT '_isbn13'::regtype::oid").Scan(&prior); err != nil {
					t.Fatal(err)
				}
				run(fixture.Exec("CREATE EXTENSION isn", 0))
				isbnIdentity(t, p, b, c, "public")
				run(fixture.Select("SELECT oid=$1::oid AS retained,typtype FROM pg_type WHERE oid='_isbn13'::regtype::oid", []string{"retained", "typtype"}, []string{"BOOL", "CHAR"}, [][]any{{true, tc.kind}}, prior))
				run(fixture.Select("SELECT typarray<>$1::oid AS independent FROM pg_type WHERE oid='isbn13'::regtype::oid", []string{"independent"}, []string{"BOOL"}, [][]any{{true}}, prior))
			})
		})
	}
}
