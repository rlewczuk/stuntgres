package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGConstraintLifecycle(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pc_life_schema")
	pgConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.PGClassExec(t, p, b, b.DB, "CREATE TABLE pc_life(a INT,dead TEXT,b INT,CONSTRAINT pc_life_u UNIQUE(b,a))", "ALTER TABLE pc_life DROP COLUMN dead", "CREATE TABLE pc_life_public_anchor(marker INT)")
		id := pgConstraintID(t, p, b.DB, "pc_life", "pc_life_u")
		q := "SELECT conname,conkey::text,connamespace=(SELECT relnamespace FROM pg_class WHERE oid=$2::regclass::oid) AS namespace,connamespace=(SELECT relnamespace FROM pg_class WHERE oid='public.pc_life_public_anchor'::regclass::oid) AS public_namespace,connamespace<>0::oid AS namespace_present FROM pg_constraint WHERE oid=$1"
		held := fixture.ViewHold(t, p, b, q)
		check := func(name, namespaceAnchor string, public bool) {
			pgConstraintCheck(t, p, b, held, fixture.Select(q, []string{"conname", "conkey", "namespace", "public_namespace", "namespace_present"}, []string{"NAME", "TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{name, "{3,1}", true, public, true}}, id, namespaceAnchor))
		}
		check("pc_life_u", "public.pc_life", true)
		var oldRows interface{ Close() error }
		if !b.PG {
			rows, err := b.DB.QueryContext(p.Ctx, "SELECT * FROM pg_constraint WHERE oid=$1", id)
			if err != nil {
				t.Fatal(err)
			}
			oldRows = rows
			defer oldRows.Close()
			pgConstraintMetadata(t, rows, false)
			fixture.PGClassExec(t, p, b, b.DB, "ALTER TABLE pc_life RENAME CONSTRAINT pc_life_u TO pc_life_new")
			if !rows.Next() {
				t.Fatal("retained row missing", rows.Err())
			}
			values := make([]any, 28)
			targets := make([]any, 28)
			for i := range values {
				targets[i] = &values[i]
			}
			if err := rows.Scan(targets...); err != nil {
				t.Fatal(err)
			}
			if values[1] != "pc_life_u" || values[20] != "{3,1}" {
				t.Fatal("retained values", values)
			}
		} else {
			fixture.PGClassExec(t, p, b, b.DB, "ALTER TABLE pc_life RENAME CONSTRAINT pc_life_u TO pc_life_new")
		}
		check("pc_life_new", "public.pc_life", true)
		// The destination anchor supplies an independent namespace identity
		// through the already supported pg_class/regclass public APIs.
		fixture.PGClassExec(t, p, b, b.DB, "ALTER TABLE pc_life RENAME COLUMN b TO key_b", "ALTER TABLE pc_life RENAME TO pc_life_renamed", "ALTER INDEX pc_life_new RENAME TO pc_life_index", "CREATE SCHEMA pc_life_schema", "CREATE TABLE pc_life_schema.pc_life_namespace_anchor(marker INT)", "ALTER TABLE pc_life_renamed SET SCHEMA pc_life_schema")
		check("pc_life_index", "pc_life_schema.pc_life_namespace_anchor", false)
		if got := pgConstraintID(t, p, b.DB, "pc_life_schema.pc_life_renamed", "pc_life_index"); got != id {
			t.Fatal("rename changed identity", got, id)
		}
		fixture.PGClassExec(t, p, b, b.DB, "ALTER TABLE pc_life_schema.pc_life_renamed DROP CONSTRAINT pc_life_index", "ALTER TABLE pc_life_schema.pc_life_renamed ADD CONSTRAINT pc_life_index UNIQUE(key_b,a)")
		if got := pgConstraintID(t, p, b.DB, "pc_life_schema.pc_life_renamed", "pc_life_index"); got == id {
			t.Fatal("recreated constraint reused identity")
		}
		pgConstraintCheck(t, p, b, b.DB, fixture.Select("SELECT conname FROM pg_constraint WHERE oid=$1", []string{"conname"}, []string{"NAME"}, nil, id))
		fixture.PGClassExec(t, p, b, b.DB, "CREATE TABLE pc_life_parent(a INT)", "CREATE UNIQUE INDEX pc_life_provider ON pc_life_parent(a)", "CREATE TABLE pc_life_child(a INT REFERENCES pc_life_parent(a))")
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pc_life_parent VALUES(1)", 1))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pc_life_child VALUES(1)", 1))
		fixture.PGClassExec(t, p, b, b.DB, "DROP INDEX pc_life_provider CASCADE")
		pgConstraintCheck(t, p, b, b.DB, fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='pc_life_child'::regclass::oid", []string{"conname"}, []string{"NAME"}, nil))
		pgConstraintCheck(t, p, b, b.DB, fixture.Select("SELECT a FROM pc_life_child", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
	})
}
