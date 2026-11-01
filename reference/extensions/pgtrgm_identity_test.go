package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func pgTrgmStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	run := pgTrgmRun(t, p, b, c)
	run(fixture.Exec("CREATE SCHEMA trgm_addon", 0))
	run(fixture.Exec("CREATE EXTENSION pg_trgm SCHEMA trgm_addon", 0))
	run(fixture.Exec("CREATE TABLE trgm_source(id int,v text,score real DEFAULT trgm_addon.similarity('a','aa'),trigrams text[] GENERATED ALWAYS AS (trgm_addon.show_trgm(v)) STORED,ordinary real GENERATED ALWAYS AS (trgm_addon.similarity(v,'cat')) STORED,word_score real GENERATED ALWAYS AS (trgm_addon.word_similarity(v,'cat')) STORED,strict_score real GENERATED ALWAYS AS (trgm_addon.strict_word_similarity(v,'cat')) STORED,CONSTRAINT trgm_valid CHECK(trgm_addon.similarity(v,'cat')>=0))", 0))
	run(fixture.Exec("INSERT INTO trgm_source(id,v) VALUES(1,'cat'),(2,'dog'),(3,NULL)", 3))
	run(fixture.Exec("CREATE VIEW trgm_values AS SELECT id,v,score,trigrams,ordinary,word_score,strict_score,trgm_addon.show_trgm(v) AS actual,trgm_addon.similarity(v,'cat') AS fresh,v OPERATOR(trgm_addon.%) 'cat' AS matches FROM trgm_source", 0))
	run(fixture.Exec("CREATE VIEW trgm_literals AS SELECT trgm_addon.show_trgm('cat') AS a,trgm_addon.word_similarity('a','aa') AS w,trgm_addon.strict_word_similarity('a','aa') AS s", 0))
	check := fixture.Select("SELECT id,v,score,trigrams,ordinary,word_score,strict_score,actual,fresh,matches FROM trgm_values ORDER BY id", []string{"id", "v", "score", "trigrams", "ordinary", "word_score", "strict_score", "actual", "fresh", "matches"}, []string{"INT4", "TEXT", "FLOAT4", "_TEXT", "FLOAT4", "FLOAT4", "FLOAT4", "_TEXT", "FLOAT4", "BOOL"}, [][]any{
		{int64(1), "cat", float64(0.25), pgTrgmArray("  c", " ca", "at ", "cat"), float64(1), float64(1), float64(1), pgTrgmArray("  c", " ca", "at ", "cat"), float64(1), true},
		{int64(2), "dog", float64(0.25), pgTrgmArray("  d", " do", "dog", "og "), float64(0), float64(0), float64(0), pgTrgmArray("  d", " do", "dog", "og "), float64(0), false},
		{int64(3), nil, float64(0.25), nil, nil, nil, nil, nil, nil, nil},
	})
	run(check)
	run(fixture.Prepared(check))
	run(fixture.Select("SELECT a,w,s FROM trgm_literals", []string{"a", "w", "s"}, []string{"_TEXT", "FLOAT4", "FLOAT4"}, [][]any{{pgTrgmArray("  c", " ca", "at ", "cat"), float64(0.5), float64(0.25)}}))
	return check
}

func TestReferencePgTrgmIdentityControls(t *testing.T) {
	t.Run("RenameMoveDetachShadowRelocate", func(t *testing.T) {
		pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			check := pgTrgmStored(t, p, b, c)
			run := pgTrgmRun(t, p, b, c)
			run(fixture.Exec("CREATE SCHEMA trgm_moved", 0))
			q := "SELECT trgm_addon.similarity($1,$2)"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pgTrgmScalar(b, q, "similarity", "FLOAT4", float64(0.25), "a", "aa"))
			run(fixture.Exec("ALTER FUNCTION trgm_addon.similarity(text,text) RENAME TO original_similarity", 0))
			run(fixture.Exec("ALTER FUNCTION trgm_addon.original_similarity(text,text) SET SCHEMA trgm_moved", 0))
			run(fixture.Exec("ALTER EXTENSION pg_trgm DROP FUNCTION trgm_moved.original_similarity(text,text)", 0))
			run(fixture.Select("SELECT count(*) FROM pg_depend WHERE classid=1255 AND objid='trgm_moved.original_similarity(text,text)'::regprocedure::oid AND refclassid=3079 AND deptype='e'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			run(fixture.Exec("CREATE FUNCTION trgm_addon.similarity(text,text) RETURNS real LANGUAGE SQL IMMUTABLE STRICT AS 'SELECT 0.875::real'", 0))
			run(pgTrgmScalar(b, "SELECT trgm_addon.similarity('a','aa')", "similarity", "FLOAT4", float64(0.875)))
			run(pgTrgmScalar(b, "SELECT trgm_moved.original_similarity('a','aa')", "original_similarity", "FLOAT4", float64(0.25)))
			// Stored syntax retains actual provider identity, never mutable name.
			run(check)
			run(fixture.Prepared(check))
			// Held SQL follows the ordinary invalidation/rebind contract; the
			// newly visible ordinary shadow does not acquire provider authority.
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pgTrgmScalar(b, q, "similarity", "FLOAT4", float64(0.875), "a", "aa"))
			run(fixture.Exec("DROP FUNCTION trgm_addon.similarity(text,text)", 0))
			run(fixture.Exec("ALTER EXTENSION pg_trgm ADD FUNCTION trgm_moved.original_similarity(text,text)", 0))
			run(fixture.Exec("ALTER FUNCTION trgm_moved.original_similarity(text,text) SET SCHEMA trgm_addon", 0))
			run(fixture.Exec("ALTER FUNCTION trgm_addon.original_similarity(text,text) RENAME TO similarity", 0))
			run(fixture.Exec("ALTER OPERATOR trgm_addon.% (text,text) SET SCHEMA trgm_moved", 0))
			run(check)
			run(pgTrgmScalar(b, "SELECT 'cat' OPERATOR(trgm_moved.%) 'cat' AS result", "result", "BOOL", true))
			run(fixture.Exec("ALTER OPERATOR trgm_moved.% (text,text) SET SCHEMA trgm_addon", 0))
			run(fixture.Exec("ALTER EXTENSION pg_trgm SET SCHEMA trgm_moved", 0))
			run(check)
			run(pgTrgmScalar(b, "SELECT trgm_moved.similarity('a','aa')", "similarity", "FLOAT4", float64(0.25)))
			run(fixture.Exec("SET pg_trgm.similarity_threshold=0", 0))
			live := check
			live.Rows = [][]any{append([]any(nil), check.Rows[0]...), append([]any(nil), check.Rows[1]...), append([]any(nil), check.Rows[2]...)}
			live.Rows[1][9] = true
			run(live)
			run(fixture.Exec("RESET pg_trgm.similarity_threshold", 0))
			run(check)
			run(fixture.Exec("UPDATE trgm_source SET v='cat' WHERE id=2", 1))
			run(fixture.Select("SELECT ordinary,word_score,strict_score,trigrams,fresh,matches FROM trgm_values WHERE id=2", []string{"ordinary", "word_score", "strict_score", "trigrams", "fresh", "matches"}, []string{"FLOAT4", "FLOAT4", "FLOAT4", "_TEXT", "FLOAT4", "BOOL"}, [][]any{{float64(1), float64(1), float64(1), pgTrgmArray("  c", " ca", "at ", "cat"), float64(1), true}}))
			pgTrgmNotices(t, notices)
		})
	})
	t.Run("StableGeneratedRejection", func(t *testing.T) {
		pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			run := pgTrgmRun(t, p, b, c)
			run(fixture.Exec("CREATE EXTENSION pg_trgm", 0))
			run(fixture.ImplicitError("CREATE TABLE trgm_bad(v text,b boolean GENERATED ALWAYS AS (v % 'cat') STORED)", fixture.Diagnostic{Code: "42P17", Message: "generation expression is not immutable"}))
			run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='trgm_bad' AND relnamespace='public'::regnamespace::oid", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			pgTrgmNotices(t, notices)
		})
	})
	t.Run("NativeControls", func(t *testing.T) {
		pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			run := pgTrgmRun(t, p, b, c)
			run(fixture.Exec("CREATE USER trgm_owner SUPERUSER", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION trgm_owner", 0))
			check := pgTrgmStored(t, p, b, c)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			if !b.PG {
				pgTrgmControls(t, p, b, c, check, true)
			}
			pgTrgmNotices(t, notices)
		})
	})
}
