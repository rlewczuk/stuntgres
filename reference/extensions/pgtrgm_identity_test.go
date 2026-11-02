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
	run(fixture.Exec(`CREATE TABLE trgm_source(
		id int,v text,score real DEFAULT trgm_addon.similarity('a','aa'),
		trigrams text[] GENERATED ALWAYS AS (trgm_addon.show_trgm(v)) STORED,
		ordinary real GENERATED ALWAYS AS (trgm_addon.similarity(v,'cat')) STORED,
		word_score real GENERATED ALWAYS AS (trgm_addon.word_similarity(v,'cat')) STORED,
		strict_score real GENERATED ALWAYS AS (trgm_addon.strict_word_similarity(v,'cat')) STORED,
		CONSTRAINT trgm_valid CHECK(trgm_addon.similarity(v,'cat')>=0),
		distance_input text DEFAULT 'two words',
		distance_default real DEFAULT trgm_addon.similarity_dist('a','aa'),
		distance_ordinary real GENERATED ALWAYS AS ('word' OPERATOR(trgm_addon.<->) distance_input) STORED,
		distance_word real GENERATED ALWAYS AS ('word' OPERATOR(trgm_addon.<<->) distance_input) STORED,
		distance_word_reverse real GENERATED ALWAYS AS ('word' OPERATOR(trgm_addon.<->>) distance_input) STORED,
		distance_strict real GENERATED ALWAYS AS ('word' OPERATOR(trgm_addon.<<<->) distance_input) STORED,
		distance_strict_reverse real GENERATED ALWAYS AS ('word' OPERATOR(trgm_addon.<->>>) distance_input) STORED,
		CONSTRAINT trgm_distances_valid CHECK(
			('word' OPERATOR(trgm_addon.<->) distance_input)>=0 AND
			('word' OPERATOR(trgm_addon.<<->) distance_input)>=0 AND
			('word' OPERATOR(trgm_addon.<->>) distance_input)>=0 AND
			('word' OPERATOR(trgm_addon.<<<->) distance_input)>=0 AND
			('word' OPERATOR(trgm_addon.<->>>) distance_input)>=0))`, 0))
	run(fixture.Exec("INSERT INTO trgm_source(id,v,distance_input) VALUES(1,'cat','two words'),(2,'dog','two words'),(3,NULL,NULL)", 3))
	run(fixture.Exec(`CREATE VIEW trgm_values AS SELECT
		id,v,score,trigrams,ordinary,word_score,strict_score,trgm_addon.show_trgm(v) AS actual,
		trgm_addon.similarity(v,'cat') AS fresh,v OPERATOR(trgm_addon.%) 'cat' AS matches,
		distance_input,distance_default,distance_ordinary,distance_word,distance_word_reverse,distance_strict,distance_strict_reverse,
		trgm_addon.similarity_dist('word',distance_input) AS backing_ordinary,
		trgm_addon.word_similarity_dist_op('word',distance_input) AS backing_word,
		trgm_addon.word_similarity_dist_commutator_op('word',distance_input) AS backing_word_reverse,
		trgm_addon.strict_word_similarity_dist_op('word',distance_input) AS backing_strict,
		trgm_addon.strict_word_similarity_dist_commutator_op('word',distance_input) AS backing_strict_reverse
		FROM trgm_source`, 0))
	run(fixture.Exec("CREATE VIEW trgm_literals AS SELECT trgm_addon.show_trgm('cat') AS a,trgm_addon.word_similarity('a','aa') AS w,trgm_addon.strict_word_similarity('a','aa') AS s,'word' OPERATOR(trgm_addon.<<->) 'two words' AS d,'word' OPERATOR(trgm_addon.<->>>) 'two words' AS r", 0))
	check := fixture.Select(`SELECT id,v,score,trigrams,ordinary,word_score,strict_score,actual,fresh,matches,
		distance_input,distance_default,distance_ordinary,distance_word,distance_word_reverse,distance_strict,distance_strict_reverse,
		backing_ordinary,backing_word,backing_word_reverse,backing_strict,backing_strict_reverse FROM trgm_values ORDER BY id`,
		[]string{"id", "v", "score", "trigrams", "ordinary", "word_score", "strict_score", "actual", "fresh", "matches",
			"distance_input", "distance_default", "distance_ordinary", "distance_word", "distance_word_reverse", "distance_strict", "distance_strict_reverse",
			"backing_ordinary", "backing_word", "backing_word_reverse", "backing_strict", "backing_strict_reverse"},
		[]string{"INT4", "TEXT", "FLOAT4", "_TEXT", "FLOAT4", "FLOAT4", "FLOAT4", "_TEXT", "FLOAT4", "BOOL",
			"TEXT", "FLOAT4", "FLOAT4", "FLOAT4", "FLOAT4", "FLOAT4", "FLOAT4", "FLOAT4", "FLOAT4", "FLOAT4", "FLOAT4", "FLOAT4"}, [][]any{
		{int64(1), "cat", float64(0.25), pgTrgmArray("  c", " ca", "at ", "cat"), float64(1), float64(1), float64(1), pgTrgmArray("  c", " ca", "at ", "cat"), float64(1), true},
		{int64(2), "dog", float64(0.25), pgTrgmArray("  d", " do", "dog", "og "), float64(0), float64(0), float64(0), pgTrgmArray("  d", " do", "dog", "og "), float64(0), false},
		{int64(3), nil, float64(0.25), nil, nil, nil, nil, nil, nil, nil},
	})
	for i := range check.Rows {
		var input any = "two words"
		distances := pgTrgmDistanceValues(b)
		if i == 2 {
			input = nil
			distances = make([]any, 5)
		}
		check.Rows[i] = append(check.Rows[i], input, float64(0.75))
		check.Rows[i] = append(check.Rows[i], distances...)
		check.Rows[i] = append(check.Rows[i], distances...)
	}
	run(check)
	run(fixture.Prepared(check))
	distances := pgTrgmDistanceValues(b)
	run(fixture.Select("SELECT a,w,s,d,r FROM trgm_literals", []string{"a", "w", "s", "d", "r"}, []string{"_TEXT", "FLOAT4", "FLOAT4", "FLOAT4", "FLOAT4"}, [][]any{{pgTrgmArray("  c", " ca", "at ", "cat"), float64(0.5), float64(0.25), distances[1], distances[4]}}))
	return check
}

func pgTrgmStoredWrites(b fixture.Backend, run func(fixture.Step), check fixture.Step) {
	run(fixture.Exec("UPDATE trgm_source SET v='cat' WHERE id=2", 1))
	run(fixture.Select("SELECT ordinary,word_score,strict_score,trigrams,fresh,matches FROM trgm_values WHERE id=2", []string{"ordinary", "word_score", "strict_score", "trigrams", "fresh", "matches"}, []string{"FLOAT4", "FLOAT4", "FLOAT4", "_TEXT", "FLOAT4", "BOOL"}, [][]any{{float64(1), float64(1), float64(1), pgTrgmArray("  c", " ca", "at ", "cat"), float64(1), true}}))
	run(fixture.Exec("UPDATE trgm_source SET distance_input='word' WHERE id=2", 1))
	run(fixture.Exec("INSERT INTO trgm_source(id,v) VALUES(4,'cat')", 1))
	run(fixture.Select("SELECT id,score,ordinary,word_score,strict_score,trigrams FROM trgm_source WHERE id>=2 ORDER BY id", []string{"id", "score", "ordinary", "word_score", "strict_score", "trigrams"}, []string{"INT4", "FLOAT4", "FLOAT4", "FLOAT4", "FLOAT4", "_TEXT"}, [][]any{{int64(2), float64(0.25), float64(1), float64(1), float64(1), pgTrgmArray("  c", " ca", "at ", "cat")}, {int64(3), float64(0.25), nil, nil, nil, nil}, {int64(4), float64(0.25), float64(1), float64(1), float64(1), pgTrgmArray("  c", " ca", "at ", "cat")}}))
	// Copy all rows: the control baseline still describes the original dump.
	written := check
	written.Rows = make([][]any, 4)
	for i, row := range check.Rows {
		written.Rows[i] = append([]any(nil), row...)
	}
	written.Rows[1] = append([]any(nil), check.Rows[0]...)
	written.Rows[1][0] = int64(2)
	written.Rows[1][10] = "word"
	for i := 12; i < len(written.Rows[1]); i++ {
		written.Rows[1][i] = pgTrgmFloat(b.PG, 0)
	}
	written.Rows[3] = append([]any(nil), check.Rows[0]...)
	written.Rows[3][0] = int64(4)
	run(written)
	run(fixture.Prepared(written))
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
			distances := pgTrgmDistanceValues(b)
			distanceIndex := 0
			for _, op := range pgTrgmOperators {
				if op.result != "FLOAT4" {
					continue
				}
				want := distances[distanceIndex]
				distanceIndex++
				t.Run(op.routine, func(t *testing.T) {
					run := pgTrgmRun(t, p, b, c)
					q := "SELECT trgm_addon." + op.routine + "($1,$2) AS result"
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					frame := pgTrgmScalar(b, q, "result", "FLOAT4", want, "word", "two words")
					fixture.RunStep(t, p.Ctx, held, b.Recorder, frame)
					original := "original_" + op.routine
					run(fixture.Exec("ALTER FUNCTION trgm_addon."+op.routine+"(text,text) RENAME TO "+original, 0))
					run(fixture.Exec("ALTER FUNCTION trgm_addon."+original+"(text,text) SET SCHEMA trgm_moved", 0))
					run(fixture.Exec("ALTER EXTENSION pg_trgm DROP FUNCTION trgm_moved."+original+"(text,text)", 0))
					run(fixture.Select("SELECT count(*) FROM pg_depend WHERE classid=1255 AND objid=$1::regprocedure::oid AND refclassid=3079 AND refobjid=(SELECT oid FROM pg_extension WHERE extname='pg_trgm') AND deptype='e'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, "trgm_moved."+original+"(text,text)"))
					run(fixture.Exec("CREATE FUNCTION trgm_addon."+op.routine+"(text,text) RETURNS real LANGUAGE SQL IMMUTABLE STRICT AS 'SELECT 0.875::real'", 0))
					run(pgTrgmScalar(b, "SELECT trgm_addon."+op.routine+"('word','two words') AS result", "result", "FLOAT4", float64(0.875)))
					run(pgTrgmScalar(b, "SELECT trgm_moved."+original+"('word','two words') AS result", "result", "FLOAT4", want))
					run(check)
					run(fixture.Prepared(check))
					// Execute the stored default/generated/CHECK recipes while the
					// public name is occupied by the distinguishable SQL shadow.
					run(fixture.Exec("INSERT INTO trgm_source(id,v) VALUES(4,'cat')", 1))
					inserted := check
					inserted.Rows = append([][]any(nil), check.Rows...)
					row := append([]any(nil), check.Rows[0]...)
					row[0] = int64(4)
					inserted.Rows = append(inserted.Rows, row)
					run(inserted)
					run(fixture.Exec("DELETE FROM trgm_source WHERE id=4", 1))
					shadow := frame
					shadow.Rows = [][]any{{float64(0.875)}}
					fixture.RunStep(t, p.Ctx, held, b.Recorder, shadow)
					run(fixture.Exec("DROP FUNCTION trgm_addon."+op.routine+"(text,text)", 0))
					run(fixture.Exec("ALTER EXTENSION pg_trgm ADD FUNCTION trgm_moved."+original+"(text,text)", 0))
					run(fixture.Exec("ALTER FUNCTION trgm_moved."+original+"(text,text) SET SCHEMA trgm_addon", 0))
					run(fixture.Exec("ALTER FUNCTION trgm_addon."+original+"(text,text) RENAME TO "+op.routine, 0))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, frame)
					run(fixture.Exec("ALTER OPERATOR trgm_addon."+op.token+" (text,text) SET SCHEMA trgm_moved", 0))
					run(check)
					run(fixture.Prepared(check))
					run(pgTrgmScalar(b, "SELECT 'word' OPERATOR(trgm_moved."+op.token+") 'two words' AS result", "result", "FLOAT4", want))
					run(fixture.Exec("ALTER OPERATOR trgm_moved."+op.token+" (text,text) SET SCHEMA trgm_addon", 0))
				})
			}
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
			pgTrgmStoredWrites(b, run, check)
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
