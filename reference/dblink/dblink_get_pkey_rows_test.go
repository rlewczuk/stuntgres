package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDblinkGetPkeyRows(t *testing.T) {
	pkeyCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE TABLE pkey_order(gone text,a integer,b text,c integer,CONSTRAINT pkey_order_pk PRIMARY KEY(c,a))",
			"ALTER TABLE pkey_order DROP COLUMN gone",
			"CREATE TABLE pkey_single(id integer PRIMARY KEY)",
			"CREATE TABLE pkey_none(id integer,payload text)",
			"CREATE TABLE pkey_unique(id integer UNIQUE)",
			"CREATE TABLE pkey_cover(a integer,b integer,payload text)",
			"CREATE UNIQUE INDEX pkey_cover_pk ON pkey_cover(b,a) INCLUDE(payload)",
			"ALTER TABLE pkey_cover ADD CONSTRAINT pkey_cover_pk PRIMARY KEY USING INDEX pkey_cover_pk",
		} {
			run(fixture.Exec(q, 0))
		}
		held := pkeyHeld(t, p, c, pkeyCall)
		check := func(name string, arg any, want [][]any) {
			t.Run(name, func(t *testing.T) {
				s := pkeyRows(want, arg)
				s.CheckMetadata = pkeyMetadata(b.PG)
				pkeyRoutes(t, p, b, c, &held, s)
			})
		}
		want := [][]any{{int64(1), "c"}, {int64(2), "a"}}
		check("empty_composite", "pkey_order", want)
		run(fixture.Exec("INSERT INTO pkey_order VALUES(11,'payload',33),(22,'other',44)", 2))
		check("populated_composite", "pkey_order", want)
		check("single", "pkey_single", [][]any{{int64(1), "id"}})
		check("empty_no_key", "pkey_none", nil)
		run(fixture.Exec("INSERT INTO pkey_none VALUES(1,'not a key')", 1))
		check("populated_no_key", "pkey_none", nil)
		check("unique_is_not_primary", "pkey_unique", nil)
		run(fixture.Select("SELECT indnkeyatts,indnatts FROM pg_index WHERE indexrelid='pkey_cover_pk'::regclass", []string{"indnkeyatts", "indnatts"}, []string{"INT2", "INT2"}, [][]any{{int64(2), int64(3)}}))
		check("covering_primary_key_empty", "pkey_cover", [][]any{{int64(1), "b"}, {int64(2), "a"}})
		run(fixture.Exec("INSERT INTO pkey_cover VALUES(11,22,'not a key')", 1))
		check("covering_primary_key_populated", "pkey_cover", [][]any{{int64(1), "b"}, {int64(2), "a"}})
		check("null_from", nil, nil)
		t.Run("target_list", func(t *testing.T) {
			// The cast keeps the target-list SRF form while making its wire
			// transport TEXT rather than a driver-specific user-type codec.
			const q = "SELECT pk_ext.dblink_get_pkey($1::text)::text AS key"
			s := fixture.Select(q, []string{"key"}, []string{"TEXT"}, nil, nil)
			pkeyRoutes(t, p, b, c, nil, s)
			s = fixture.Select(q, []string{"key"}, []string{"TEXT"}, [][]any{{"(1,c)"}, {"(2,a)"}}, "pkey_order")
			s.Bag = true
			pkeyRoutes(t, p, b, c, nil, s)
		})
		run(fixture.Exec(`ALTER TABLE pkey_order RENAME COLUMN c TO "C new"`, 0))
		check("renamed_column", "pkey_order", [][]any{{int64(1), "C new"}, {int64(2), "a"}})
		run(fixture.Exec("ALTER TABLE pkey_order RENAME CONSTRAINT pkey_order_pk TO renamed_pkey", 0))
		check("renamed_constraint", "PKEY_ORDER", [][]any{{int64(1), "C new"}, {int64(2), "a"}})
		run(fixture.Exec("ALTER TABLE pkey_order DROP CONSTRAINT renamed_pkey", 0))
		check("dropped_primary_key", "pkey_order", nil)
		run(fixture.Select("SELECT a,b,\"C new\" FROM pkey_order ORDER BY a", []string{"a", "b", "C new"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(11), "payload", int64(33)}, {int64(22), "other", int64(44)}}))
		for _, q := range []string{
			"CREATE SCHEMA pk_quoted",
			`CREATE TABLE pk_quoted."Mixed.Table ""name"("First Key" integer,"Second.Key" integer,PRIMARY KEY("Second.Key","First Key"))`,
			"CREATE SCHEMA pk_left", "CREATE SCHEMA pk_right",
			"CREATE TABLE pk_left.pkey_same(left_key integer PRIMARY KEY)",
			"CREATE TABLE pk_right.pkey_same(right_key integer PRIMARY KEY)",
			"SET search_path=pk_left,pk_right,public",
		} {
			run(fixture.Exec(q, 0))
		}
		check("quoted_identifiers", `"pk_quoted"."Mixed.Table ""name"`, [][]any{{int64(1), "Second.Key"}, {int64(2), "First Key"}})
		check("caller_path_left", "pkey_same", [][]any{{int64(1), "left_key"}})
		run(fixture.Exec("SET search_path=pk_right,pk_left,public", 0))
		check("caller_path_right", "pkey_same", [][]any{{int64(1), "right_key"}})
		check("qualified_ignores_shadow", "pk_left.pkey_same", [][]any{{int64(1), "left_key"}})
		run(fixture.Exec("SET search_path=public", 0))
	})
}
