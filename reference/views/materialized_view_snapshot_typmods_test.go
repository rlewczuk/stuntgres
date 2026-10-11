package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMaterializedViewSnapshotTypmods(t *testing.T) {
	cases := []struct {
		name, declaration, baselineLiteral, baseline string
		expressions                                  []struct{ name, sql, text string }
	}{
		{"numeric", "NUMERIC(5,2)", "12.34", "12.34", []struct{ name, sql, text string }{
			{"assignment", "12.345", "12.345"},
			{"explicit", "CAST(12.345 AS NUMERIC(5,2))", "12.35"},
			{"nested", "CAST(CAST(12.345 AS NUMERIC(5,2)) AS NUMERIC)", "12.35"},
			{"colon", "12.345::numeric(5,2)", "12.35"},
		}},
		{"character", "VARCHAR(4)", "'abcd'", "abcd", []struct{ name, sql, text string }{
			{"assignment", "'abcdef'", "abcdef"},
			{"explicit", "CAST('abcdef' AS VARCHAR(4))", "abcd"},
			{"nested", "CAST(CAST('abcdef' AS VARCHAR(4)) AS TEXT)", "abcd"},
			{"spaces", "CAST('abcd ' AS VARCHAR(4))", "abcd"},
			{"unicode", "CAST('é界🙂xy' AS VARCHAR(4))", "é界🙂x"},
		}},
		{"time", "TIME(3)", "TIME '12:34:56.123'", "12:34:56.123", []struct{ name, sql, text string }{
			{"assignment", "TIME '12:34:56.123789'", "12:34:56.123789"},
			{"explicit", "CAST(TIME '12:34:56.123789' AS TIME(3))", "12:34:56.124"},
			{"nested", "CAST(CAST(TIME '12:34:56.123789' AS TIME(3)) AS TIME(6))", "12:34:56.124"},
			{"typed", "TIME(3) '12:34:56.123789'", "12:34:56.124"},
		}},
		{"timestamp", "TIMESTAMP(3)", "TIMESTAMP '2024-01-02 12:34:56.123'", "2024-01-02 12:34:56.123", []struct{ name, sql, text string }{
			{"assignment", "TIMESTAMP '2024-01-02 12:34:56.123789'", "2024-01-02 12:34:56.123789"},
			{"explicit", "CAST(TIMESTAMP '2024-01-02 12:34:56.123789' AS TIMESTAMP(3))", "2024-01-02 12:34:56.124"},
			{"nested", "CAST(CAST(TIMESTAMP '2024-01-02 12:34:56.123789' AS TIMESTAMP(3)) AS TIMESTAMP(6))", "2024-01-02 12:34:56.124"},
			{"typed", "TIMESTAMP(3) '2024-01-02 12:34:56.123789'", "2024-01-02 12:34:56.124"},
			{"carry", "CAST(TIMESTAMP '2024-01-02 12:34:56.999789' AS TIMESTAMP(3))", "2024-01-02 12:34:57"},
		}},
		{"timestamptz", "TIMESTAMPTZ(3)", "TIMESTAMPTZ '2024-01-02 12:34:56.123+00'", "2024-01-02 12:34:56.123+00", []struct{ name, sql, text string }{
			{"assignment", "TIMESTAMPTZ '2024-01-02 12:34:56.123789+00'", "2024-01-02 12:34:56.123789+00"},
			{"explicit", "CAST(TIMESTAMPTZ '2024-01-02 12:34:56.123789+00' AS TIMESTAMPTZ(3))", "2024-01-02 12:34:56.124+00"},
			{"nested", "CAST(CAST(TIMESTAMPTZ '2024-01-02 12:34:56.123789+00' AS TIMESTAMPTZ(3)) AS TIMESTAMPTZ(6))", "2024-01-02 12:34:56.124+00"},
			{"typed", "TIMESTAMPTZ(3) '2024-01-02 12:34:56.123789+00'", "2024-01-02 12:34:56.124+00"},
		}},
	}
	for _, tc := range cases {
		for _, expression := range tc.expressions {
			t.Run(tc.name+"/"+expression.name, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						conn, run := fixture.QueryTableCreateSession(t, p, backend)
						run(fixture.Exec("CREATE TABLE mv_typmod_source(v "+tc.declaration+")", 0))
						run(fixture.Exec("INSERT INTO mv_typmod_source VALUES("+tc.baselineLiteral+")", 1))
						run(fixture.Exec("CREATE MATERIALIZED VIEW mv_typmod_sentinel AS SELECT v FROM mv_typmod_source", 1))
						run(fixture.Exec("CREATE MATERIALIZED VIEW mv_typmod_no AS SELECT v FROM mv_typmod_source WITH NO DATA", 0))
						run(fixture.Exec("INSERT INTO mv_typmod_source VALUES("+tc.baselineLiteral+")", 1))
						projection, datatype := "v::text AS v", "TEXT"
						ordinary := "(" + expression.sql + ")::text"
						if tc.name == "numeric" {
							// NUMERIC already has exact decimal driver transport. Do not
							// require the unrelated numeric-to-text explicit cast family.
							projection, datatype, ordinary = "v", "NUMERIC", expression.sql
						}
						// Ordinary SQL may intentionally quantify these casts. Native
						// Restore may not silently change an encoded persisted datum.
						run(fixture.Select("SELECT "+ordinary+" AS v", []string{"v"}, []string{datatype}, [][]any{{expression.text}}))
						check := func() {
							run(fixture.Select("SELECT "+projection+" FROM mv_typmod_sentinel", []string{"v"}, []string{datatype}, [][]any{{tc.baseline}}))
							source := fixture.Select("SELECT "+projection+" FROM mv_typmod_source", []string{"v"}, []string{datatype}, [][]any{{tc.baseline}, {tc.baseline}})
							source.Bag = true
							run(source)
							fixture.MaterializedViewPopulation(run, "mv_typmod_sentinel", true)
							fixture.MaterializedViewPopulation(run, "mv_typmod_no", false)
							run(fixture.MaterializedViewUnpopulated("mv_typmod_no"))
						}
						check()
						if backend.PG {
							return
						} // No PostgreSQL Restore API is invented.
						var genuine bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &genuine); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(genuine.Bytes())); err != nil {
							t.Fatalf("genuine typed dump failed: %v", err)
						}
						check()
						var before bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
							t.Fatal(err)
						}
						retained, err := conn.QueryContext(p.Ctx, "SELECT "+projection+" FROM mv_typmod_sentinel")
						if err != nil {
							t.Fatal(err)
						}
						defer retained.Close()
						prefix := "CREATE MATERIALIZED VIEW public.mv_import AS SELECT CAST(" + tc.baselineLiteral + " AS " + tc.declaration + ") AS v WITH NO DATA;\n"
						payload := materializedViewSnapshot("public", "mv_import", true, "INSERT INTO public.mv_import VALUES ("+expression.sql+");")
						if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(prefix+payload)); err == nil {
							t.Fatalf("Restore accepted lossy %s %s cast tree", tc.name, expression.name)
						}
						check()
						fixture.QueryTableCreateAbsent(t, run, backend.PG, "mv_import")
						var after bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
							t.Fatal(err)
						}
						if !bytes.Equal(before.Bytes(), after.Bytes()) {
							t.Fatal("rejected snapshot changed complete sentinel catalog/data/population")
						}
						if !retained.Next() {
							t.Fatalf("retained sentinel row lost: %v", retained.Err())
						}
						var value string
						if err := retained.Scan(&value); err != nil {
							t.Fatal(err)
						}
						if value != tc.baseline || retained.Next() || retained.Err() != nil {
							t.Fatalf("retained sentinel changed: %q %v", value, retained.Err())
						}
					})
				}
			})
		}
	}
}
