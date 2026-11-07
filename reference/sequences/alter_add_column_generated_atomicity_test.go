package reference_test

import (
	"bytes"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterAddColumnGeneratedAtomicity(t *testing.T) {
	for _, tc := range []struct{ name, seed, definition, code, message, column, constraint string }{
		{"late-zero", "(1),(0)", "b integer GENERATED ALWAYS AS(10/a) STORED", "22012", "division by zero", "", ""},
		{"late-overflow", "(1),(2147483647)", "b integer GENERATED ALWAYS AS(a+1) STORED", "22003", "integer out of range", "", ""},
		{"not-null", "(1),(2)", "b integer GENERATED ALWAYS AS(NULL) STORED NOT NULL", "23502", `column "b" of relation "g" contains null values`, "b", ""},
		{"check", "(1),(2)", "b integer GENERATED ALWAYS AS(a+1) STORED CONSTRAINT ck CHECK(b<3)", "23514", `check constraint "ck" of relation "g" is violated by some row`, "", "ck"},
		{"later-action", "(1),(2)", "b integer GENERATED ALWAYS AS(a+1) STORED, ADD bad integer DEFAULT 0 CONSTRAINT ck CHECK(bad>0)", "23514", `check constraint "ck" of relation "g" is violated by some row`, "", "ck"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addGeneratedRoutes(t, func(g *addGeneratedCase) {
				g.exec("CREATE TABLE g(a integer)", 0)
				g.exec("INSERT INTO g VALUES"+tc.seed, 2)
				g.exec("CREATE INDEX g_a_idx ON g(a)", 0)
				var before []byte
				if !g.b.PG {
					before = fixture.CheckDump(g.t, g.p)
				}
				d := addGeneratedDiagnostic(tc.code, tc.message)
				if tc.code == "23502" || tc.code == "23514" {
					d.Schema, d.Table, d.Column, d.Constraint = "public", "g", tc.column, tc.constraint
				}
				g.fail("ALTER TABLE g ADD "+tc.definition, d, false, "execute")
				rows := [][]any{{int64(1)}, {int64(2)}}
				if tc.name == "late-zero" {
					rows = [][]any{{int64(0)}, {int64(1)}}
				}
				if tc.name == "late-overflow" {
					rows = [][]any{{int64(1)}, {int64(2147483647)}}
				}
				g.unchanged(rows)
				g.run(fixture.Select("SELECT indexname,indexdef FROM pg_indexes WHERE tablename='g'", []string{"indexname", "indexdef"}, []string{"NAME", "TEXT"}, [][]any{{"g_a_idx", "CREATE INDEX g_a_idx ON public.g USING btree (a)"}}))
				if !g.b.PG && !bytes.Equal(before, fixture.CheckDump(g.t, g.p)) {
					g.t.Fatal("failed ADD changed native dump")
				}
				g.exec("ALTER TABLE g ADD b bigint GENERATED ALWAYS AS(a::bigint+1) STORED", 0)
				g.kinds("g", []any{"b", "s"})
			})
		})
	}
	t.Run("transactions", func(t *testing.T) {
		addGeneratedRoutes(t, func(g *addGeneratedCase) {
			g.exec("CREATE TABLE g(a integer)", 0)
			g.exec("INSERT INTO g VALUES(1),(2),(3)", 3)
			g.exec("BEGIN", 0)
			g.exec("UPDATE g SET a=8 WHERE a=1", 1)
			g.exec("DELETE FROM g WHERE a=2", 1)
			g.exec("INSERT INTO g VALUES(4)", 1)
			g.exec("SAVEPOINT before_add", 0)
			g.exec("ALTER TABLE g ADD b integer GENERATED ALWAYS AS(a+1) STORED", 0)
			g.run(fixture.Select("SELECT * FROM g ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(4)}, {int64(4), int64(5)}, {int64(8), int64(9)}}))
			g.exec("ROLLBACK TO before_add", 0)
			g.unchanged([][]any{{int64(3)}, {int64(4)}, {int64(8)}})
			g.fail("ALTER TABLE g ADD b integer GENERATED ALWAYS AS(a/0) STORED", addGeneratedDiagnostic("22012", "division by zero"), false, "execute")
			g.exec("ROLLBACK TO before_add", 0)
			g.unchanged([][]any{{int64(3)}, {int64(4)}, {int64(8)}})
			g.exec("ALTER TABLE g ADD b integer GENERATED ALWAYS AS(a+1) STORED", 0)
			g.exec("ROLLBACK", 0)
			g.unchanged([][]any{{int64(1)}, {int64(2)}, {int64(3)}})
			g.exec("BEGIN", 0)
			g.exec("UPDATE g SET a=8 WHERE a=1", 1)
			g.exec("DELETE FROM g WHERE a=2", 1)
			g.exec("INSERT INTO g VALUES(4)", 1)
			g.exec("ALTER TABLE g ADD b integer GENERATED ALWAYS AS(a+1) STORED", 0)
			g.exec("COMMIT", 0)
			g.run(fixture.Select("SELECT * FROM g ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(4)}, {int64(4), int64(5)}, {int64(8), int64(9)}}))
		})
	})
}
