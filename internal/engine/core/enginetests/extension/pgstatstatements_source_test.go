package extension_test

import (
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
)

// These are real compiled PL commands and producer lifetimes. The accumulator's
// owned representative is checked independently of its analyzed grouping key.
func TestPgStatStatementsCompletionNestedSource(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{
		"SET pg_stat_statements.track='none'",
		"CREATE TABLE phase_source(k integer)",
		"INSERT INTO phase_source VALUES(1),(2)",
		`CREATE FUNCTION phase_source_run() RETURNS integer LANGUAGE plpgsql AS $$DECLARE r record; n integer; BEGIN PERFORM /*perform 雪*/ 11 FROM phase_source; SELECT /*capture 雪*/ 12 INTO n FROM phase_source ORDER BY k LIMIT 1; FOR r IN SELECT /*loop 雪*/ k,13 AS loop_literal FROM phase_source ORDER BY k LOOP PERFORM /*body 雪*/ 14+r.k; END LOOP; EXECUTE 'SELECT /*dynamic 雪*/ 15' INTO n; RETURN (SELECT /*return 雪*/ 16 FROM phase_source LIMIT 1); END$$`,
		`CREATE FUNCTION phase_source_failed() RETURNS integer LANGUAGE plpgsql AS $$BEGIN PERFORM /*survived 雪*/ k FROM phase_source; RAISE EXCEPTION 'parent failed'; END$$`,
		"SET pg_stat_statements.track_utility=off",
		"SET pg_stat_statements.track_planning=on",
		"SET pg_stat_statements.track='all'",
	} {
		pgssOwnerExec(t, e, s, q)
	}
	pgssOwnerExec(t, e, s, "SELECT phase_source_run()")
	for _, want := range []struct {
		marker, query string
		calls, rows   int64
	}{
		{"perform 雪", "SELECT /*perform 雪*/ $1 FROM phase_source", 1, 2},
		{"capture 雪", "SELECT /*capture 雪*/ $1        FROM phase_source ORDER BY k LIMIT $2", 1, 1},
		{"loop 雪", "SELECT /*loop 雪*/ k,$1 AS loop_literal FROM phase_source ORDER BY k", 1, 2},
		{"body 雪", "SELECT /*body 雪*/ $1+r.k", 2, 2},
		{"dynamic 雪", "SELECT /*dynamic 雪*/ $1", 1, 1},
		{"return 雪", "(SELECT /*return 雪*/ $1 FROM phase_source LIMIT $2)", 1, 1},
	} {
		entry := pgssPhaseEntry(t, e, want.marker)
		if entry.Query != want.query || entry.Key.TopLevel {
			t.Fatalf("%s source/toplevel = %q/%t, want %q/false", want.marker, entry.Query, entry.Key.TopLevel, want.query)
		}
		pgssPhaseCounts(t, e, want.marker, want.calls, want.calls, want.rows)
	}
	_, err := pgssOwnerSQL(t, e, s, "SELECT phase_source_failed()")
	enginetest.State(t, err, "P0001")
	child := pgssPhaseEntry(t, e, "survived 雪")
	if child.Query != "SELECT /*survived 雪*/ k FROM phase_source" || child.Key.TopLevel {
		t.Fatal("successful child lost its own source/level", child)
	}
	pgssPhaseCounts(t, e, "survived 雪", 1, 1, 2)
	pgssPhaseCounts(t, e, "SELECT phase_source_failed()", 1, 0, 0)
	for _, entry := range e.Cluster.StatementStatistics.Snapshot() {
		if !entry.Key.TopLevel && (strings.Contains(entry.Query, "DECLARE r") || strings.Contains(entry.Query, "END LOOP") || strings.Contains(entry.Query, "phase_source_run()")) {
			t.Fatal("lexical child borrowed whole program or invocation source", entry)
		}
	}
}

func TestPgStatStatementsCompletionExpressionSource(t *testing.T) {
	for _, tc := range []struct {
		name, body, query string
		calls             int64
	}{
		{"initializer", "DECLARE n integer := (/*operand 雪*/ 101); BEGIN END", "(/*operand 雪*/ $1)", 1},
		{"assignment", "DECLARE n integer; BEGIN n := (/*operand 雪*/ 101); END", "(/*operand 雪*/ $1)", 1},
		{"if", "BEGIN IF (/*operand 雪*/ true) THEN END IF; END", "(/*operand 雪*/ $1)", 1},
		{"case_search", "BEGIN CASE (/*operand 雪*/ 101) WHEN 101 THEN END CASE; END", "(/*operand 雪*/ $1)", 1},
		{"while", "BEGIN WHILE (/*operand 雪*/ false) LOOP END LOOP; END", "(/*operand 雪*/ $1)", 1},
		{"integer_for", "BEGIN FOR n IN (/*operand 雪*/ 101)..102 LOOP EXIT; END LOOP; END", "(/*operand 雪*/ $1)", 2},
		{"foreach", "DECLARE n integer; BEGIN FOREACH n IN ARRAY ARRAY[/*operand 雪*/101] LOOP END LOOP; END", "ARRAY[/*operand 雪*/$1]", 1},
		{"exit_when", "BEGIN LOOP EXIT WHEN (/*operand 雪*/true); END LOOP; END", "(/*operand 雪*/$1)", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"SET pg_stat_statements.track='none'", "SET pg_stat_statements.track_utility=off", "SET pg_stat_statements.track_planning=on", "SET pg_stat_statements.track='all'"} {
				pgssOwnerExec(t, e, s, q)
			}
			pgssOwnerExec(t, e, s, "DO $$"+tc.body+"$$")
			entry := pgssPhaseEntry(t, e, "operand 雪")
			if entry.Query != tc.query || entry.Key.TopLevel {
				t.Fatalf("bounded native scalar source = %q/%t, want %q/false", entry.Query, entry.Key.TopLevel, tc.query)
			}
			// The native expression owner genuinely runs its ordinary query executor.
			// This does not assert PostgreSQL's separate simple-expression fast path.
			pgssPhaseCounts(t, e, "operand 雪", tc.calls, tc.calls, tc.calls)
		})
	}
}

func TestPgStatStatementsCompletionCursorArguments(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"SET pg_stat_statements.track='none'", "SET pg_stat_statements.track_utility=off", "SET pg_stat_statements.track_planning=on", "SET pg_stat_statements.track='all'"} {
		pgssOwnerExec(t, e, s, q)
	}
	pgssOwnerExec(t, e, s, `DO $$DECLARE c CURSOR(a integer,"Odd 雪" integer) FOR SELECT a+"Odd 雪"; x integer; BEGIN OPEN c("Odd 雪" => (SELECT 22), a => (SELECT /*argument 雪*/11)); FETCH c INTO x; CLOSE c; END$$`)
	entry := pgssPhaseEntry(t, e, "argument 雪")
	// Released read_cursor_args rebuilds declaration order and adds each formal's
	// AS label when any argument is named. Each selected location must move with
	// its exact original operand, not with written argument order or UTF8 chars.
	want := `(SELECT /*argument 雪*/$1) AS a, (SELECT $2) AS "Odd 雪"`
	if entry.Query != want || entry.Key.TopLevel {
		t.Fatalf("cursor argument projection source = %q/%t, want %q/false", entry.Query, entry.Key.TopLevel, want)
	}
	pgssPhaseCounts(t, e, "argument 雪", 1, 1, 1)
}
