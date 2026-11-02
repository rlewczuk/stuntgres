package extension_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/parser"
)

func TestPgStatStatementsCompletionUtilityBinding(t *testing.T) {
	for _, tc := range []struct {
		name     string
		commands []string
		rows     int64
	}{
		{"explain", []string{"EXPLAIN SELECT 11 AS phase_util_first", "EXPLAIN SELECT 22 AS phase_util_second"}, 0},
		{"declare", []string{"BEGIN", "DECLARE utility_cursor CURSOR FOR SELECT 11 AS phase_util_first", "CLOSE utility_cursor", "DECLARE utility_cursor CURSOR FOR SELECT 22 AS phase_util_second", "CLOSE utility_cursor", "COMMIT"}, 0},
		{"ctas", []string{"CREATE TABLE utility_result AS SELECT 11 AS phase_util_first", "DROP TABLE utility_result", "CREATE TABLE utility_result AS SELECT 22 AS phase_util_second"}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"SET pg_stat_statements.track='none'", "SET pg_stat_statements.track_utility=on", "SET pg_stat_statements.track_planning=on", "SET pg_stat_statements.track='all'"} {
				pgssOwnerExec(t, e, s, q)
			}
			for _, q := range tc.commands {
				pgssOwnerExec(t, e, s, q)
			}
			entry := pgssPhaseEntry(t, e, "phase_util_first")
			if !entry.Key.TopLevel || !strings.Contains(entry.Query, "SELECT $1 AS phase_util_first") {
				t.Fatal("utility did not use its real analyzed child source", entry)
			}
			pgssPhaseCounts(t, e, "phase_util_first", 0, 2, tc.rows)
			for _, candidate := range e.Cluster.StatementStatistics.Snapshot() {
				if strings.Contains(candidate.Query, "phase_util_second") {
					t.Fatal("typed child literal/output label split utility identity", candidate)
				}
			}
		})
	}
}

func TestPgStatStatementsCompletionUtilityNesting(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"SET pg_stat_statements.track='none'", "SET pg_stat_statements.track_utility=off", "SET pg_stat_statements.track_planning=on", "SET pg_stat_statements.track='top'"} {
		pgssOwnerExec(t, e, s, q)
	}
	pgssOwnerExec(t, e, s, "EXPLAIN ANALYZE SELECT 11 AS phase_hidden_child")
	pgssPhaseCounts(t, e, "phase_hidden_child", 0, 0, 0)
	pgssOwnerExec(t, e, s, "SET pg_stat_statements.track='all'")
	pgssOwnerExec(t, e, s, "EXPLAIN ANALYZE SELECT 22 AS phase_actual_child")
	child := pgssPhaseEntry(t, e, "phase_actual_child")
	if child.Key.TopLevel {
		t.Fatal("suppressed utility promoted a genuine executor child", child)
	}
	pgssPhaseCounts(t, e, "phase_actual_child", 1, 1, 1)
	pgssOwnerExec(t, e, s, "SET pg_stat_statements.track_utility=on")
	pgssOwnerExec(t, e, s, "EXPLAIN ANALYZE SELECT 33 AS phase_utility_wrapper")
	wrapper := pgssPhaseEntry(t, e, "phase_utility_wrapper")
	if !wrapper.Key.TopLevel {
		t.Fatal("wrapper lost its own utility completion", wrapper)
	}
	pgssPhaseCounts(t, e, "phase_utility_wrapper", 0, 1, 0)
	pgssPhaseCounts(t, e, "phase_actual_child", 1, 1, 1)
}

func TestPgStatStatementsCompletionUtilityRows(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		setup       []string
		affected    int64
		processed   int64
	}{
		{name: "ctas", query: "CREATE TABLE phase_population AS SELECT k FROM population_source", affected: 3, processed: 3},
		{name: "into", query: "SELECT k INTO phase_population FROM population_source", affected: 3, processed: 3},
		{name: "materialized", query: "CREATE MATERIALIZED VIEW phase_population AS SELECT k FROM population_source", affected: 3, processed: 3},
		{name: "refresh", setup: []string{"CREATE MATERIALIZED VIEW phase_population AS SELECT k FROM population_source"}, query: "REFRESH MATERIALIZED VIEW phase_population", processed: 3},
		{name: "concurrent_unchanged", setup: []string{"CREATE MATERIALIZED VIEW phase_population AS SELECT k FROM population_source", "CREATE UNIQUE INDEX population_key ON phase_population(k)"}, query: "REFRESH MATERIALIZED VIEW CONCURRENTLY phase_population", processed: 3},
		{name: "no_data", setup: []string{"CREATE MATERIALIZED VIEW phase_population AS SELECT k FROM population_source"}, query: "REFRESH MATERIALIZED VIEW phase_population WITH NO DATA"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			for _, q := range append([]string{"SET pg_stat_statements.track='none'", "CREATE TABLE population_source(k integer)", "INSERT INTO population_source VALUES(1),(2),(3)"}, tc.setup...) {
				pgssOwnerExec(t, e, s, q)
			}
			pgssOwnerExec(t, e, s, "SET pg_stat_statements.track_utility=on")
			pgssOwnerExec(t, e, s, "SET pg_stat_statements.track='all'")
			result, err := pgssOwnerSQL(t, e, s, tc.query)
			if err != nil || result.Count != tc.affected || result.UtilityRows != tc.processed {
				t.Fatal("utility completion versus public affected count", result, err)
			}
			pgssPhaseCounts(t, e, "phase_population", 0, 1, tc.processed)
			entry := pgssPhaseEntry(t, e, "phase_population")
			if !entry.Key.TopLevel || entry.Query != tc.query {
				t.Fatal("completion lost actual wrapper/source", entry)
			}
		})
	}
}

func TestPgStatStatementsCompletionCopyAPI(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"SET pg_stat_statements.track='none'", "CREATE TABLE phase_copy_api(k integer)", "SET pg_stat_statements.track_utility=on", "SET pg_stat_statements.track='all'"} {
		pgssOwnerExec(t, e, s, q)
	}
	from := "COPY phase_copy_api FROM STDIN"
	copy := func(query string, input io.Reader, output io.Writer, terminal error, priorCalls, priorRows int64) (int64, error) {
		t.Helper()
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		op.OriginalSQL, op.SQLAvailable = query, true
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		count, err := scope.Copy(enginetest.Statement(t, query).(*parser.Copy), input, output)
		pgssPhaseCounts(t, e, query, 0, priorCalls, priorRows)
		if err == nil {
			err = terminal
		}
		err = scope.Finish(err)
		if again := scope.Finish(err); !errors.Is(again, err) {
			t.Fatal("COPY Finish changed terminal identity", again, err)
		}
		if scope.Owner.CopyLease != nil || scope.Stage.Rows != nil {
			t.Fatal("completed COPY retained stream authority or private writes")
		}
		return count, err
	}
	if count, err := copy(from, strings.NewReader("1\n2\n3\n"), nil, nil, 0, 0); err != nil || count != 3 {
		t.Fatal("successful COPY", count, err)
	}
	pgssPhaseCounts(t, e, from, 0, 1, 3)
	if _, err := copy(from, strings.NewReader("4\nbad\n"), nil, nil, 1, 3); err == nil {
		t.Fatal("COPY decoder error was suppressed")
	} else {
		enginetest.State(t, err, "22P02")
	}
	pgssPhaseCounts(t, e, from, 0, 1, 3)
	veto := errors.New("actual COPY Finish veto")
	if count, err := copy(from, strings.NewReader("4\n"), nil, veto, 1, 3); count != 1 || !errors.Is(err, veto) {
		t.Fatal("COPY terminal veto", count, err)
	}
	pgssPhaseCounts(t, e, from, 0, 1, 3)
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "phase_copy_api")
	if len(e.Storage.Relations[ref.Address].Versions) != 3 {
		t.Fatal("failed COPY published its private rows")
	}
	to := "COPY phase_copy_api TO STDOUT"
	var output bytes.Buffer
	if count, err := copy(to, nil, &output, nil, 0, 0); err != nil || count != 3 || output.String() != "1\n2\n3\n" {
		t.Fatal("COPY output completion", count, err, output.String())
	}
	pgssPhaseCounts(t, e, to, 0, 1, 3)
}

func TestPgStatStatementsCompletionDatabaseReplacement(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	pgssOwnerExec(t, e, s, "SET pg_stat_statements.track_utility=off")
	pgssOwnerExec(t, e, s, "SELECT 1 AS phase_default_database")
	pgssOwnerExec(t, e, s, "CREATE DATABASE phase_database_peer")
	pgssPhaseCounts(t, e, "phase_default_database", 0, 1, 1)
	peer := e.Cluster.Targets["phase_database_peer"]
	if peer == nil || peer.Cluster != e.Cluster || peer.Catalog.DatabaseAddress() == e.Catalog.DatabaseAddress() {
		t.Fatal("template copy was not attached with its own database identity")
	}
	ps := enginetest.StartSession(t, peer, nil)
	pgssOwnerExec(t, peer, ps, "SET pg_stat_statements.track_utility=off")
	pgssOwnerExec(t, peer, ps, "SELECT 1 AS phase_peer_database")
	old := pgssPhaseEntry(t, e, "phase_peer_database")
	if old.Key.DatabaseID != uint32(peer.Catalog.DatabaseAddress().ObjectID) {
		t.Fatal("peer execution borrowed template/default database identity", old)
	}
	if err := engine.Reset(context.Background(), peer); err != nil {
		t.Fatal(err)
	}
	pgssPhaseCounts(t, e, "phase_default_database", 0, 1, 1)
	pgssPhaseCounts(t, e, "phase_peer_database", 0, 0, 0)
	if old.Execution.Count != 1 || old.Rows != 1 {
		t.Fatal("replacement mutated an owned old snapshot", old)
	}
	pgssOwnerExec(t, peer, ps, "SELECT 1 AS phase_peer_after_replace")
	if err := engine.Restore(context.Background(), peer, strings.NewReader("SELECT no_such_restore_function();")); err == nil {
		t.Fatal("invalid replacement succeeded")
	}
	pgssPhaseCounts(t, e, "phase_default_database", 0, 1, 1)
	pgssPhaseCounts(t, e, "phase_peer_after_replace", 0, 1, 1)
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	pgssPhaseCounts(t, e, "phase_default_database", 0, 0, 0)
	pgssPhaseCounts(t, e, "phase_peer_after_replace", 0, 1, 1)
}

type pgssCopyQueryWriter func([]byte) (int, error)

func (w pgssCopyQueryWriter) Write(data []byte) (int, error) { return w(data) }

func TestPgStatStatementsCompletionCopyQuery(t *testing.T) {
	for _, wire := range []bool{false, true} {
		for _, mode := range []struct {
			track, utility string
			wrapper, child bool
		}{
			{"all", "on", true, true},
			{"top", "on", true, false},
			{"all", "off", false, true},
			{"top", "off", false, false},
		} {
			for _, failure := range []string{"none", "query", "output"} {
				t.Run(fmt.Sprintf("wire_%t/%s/utility_%s/%s", wire, mode.track, mode.utility, failure), func(t *testing.T) {
					e := engine.New()
					s := enginetest.StartSession(t, e, nil)
					for _, q := range []string{"SET pg_stat_statements.track='none'", "CREATE TABLE phase_copy_query(k integer)", "INSERT INTO phase_copy_query VALUES(1),(2),(3)", "SET pg_stat_statements.track_utility=" + mode.utility, "SET pg_stat_statements.track='" + mode.track + "'"} {
						pgssOwnerExec(t, e, s, q)
					}
					query := "COPY (SELECT k FROM phase_copy_query ORDER BY k) TO STDOUT"
					if failure == "query" {
						query = "COPY (SELECT 12/(k-2) FROM phase_copy_query ORDER BY k) TO STDOUT"
					}
					op, err := s.Acquire(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					defer op.Done()
					op.OriginalSQL, op.SQLAvailable = query, true
					scope, err := e.StartStatement(op)
					if err != nil {
						t.Fatal(err)
					}
					var output bytes.Buffer
					var destination io.Writer = &output
					stopped := errors.New("genuine COPY output failure")
					if failure == "output" {
						destination = pgssCopyQueryWriter(func([]byte) (int, error) { return 0, stopped })
					}
					syntax := enginetest.Statement(t, query).(*parser.Copy)
					var count int64
					if wire {
						var result core.Result
						result, err = scope.ExecuteProtocolSimple(syntax, nil, nil, nil, destination, core.CopyProtocolHooks{})
						count = result.Count
						if err == nil && result.UtilityRows != count {
							t.Fatal("wire COPY lost actual utility completion", result)
						}
					} else {
						count, err = scope.Copy(syntax, nil, destination)
					}
					assertFrames := func(finished bool) {
						t.Helper()
						want := map[bool]bool{
							false: failure == "none" && mode.child,
							true:  failure == "none" && mode.wrapper && finished,
						}
						seen := map[bool]int64{}
						for _, entry := range e.Cluster.StatementStatistics.Snapshot() {
							if !strings.Contains(entry.Query, "phase_copy_query") {
								continue
							}
							if !want[entry.Key.TopLevel] || seen[entry.Key.TopLevel] != 0 || entry.Query != query || entry.Key.QueryID == 0 || entry.Execution.Count != 1 || entry.Rows != 3 || entry.Planning.Count != 0 {
								t.Fatal("COPY did not retain exactly its real wrapper/child completions", finished, entry, want)
							}
							seen[entry.Key.TopLevel] = entry.Key.QueryID
						}
						for top, expected := range want {
							if (seen[top] != 0) != expected {
								t.Fatal("missing or unexpected COPY completion", finished, top, seen, want)
							}
						}
						if seen[false] != 0 && seen[false] == seen[true] {
							t.Fatal("utility and analyzed executor borrowed the same identity")
						}
					}
					assertFrames(false)
					err = scope.Finish(err)
					switch failure {
					case "none":
						if err != nil || count != 3 || output.String() != "1\n2\n3\n" {
							t.Fatal("successful actual COPY query", count, err, output.String())
						}
					case "query":
						enginetest.State(t, err, "22012")
					case "output":
						if !errors.Is(err, stopped) {
							t.Fatal("COPY suppressed output failure", err)
						}
					}
					assertFrames(true)
					if again := scope.Finish(err); !errors.Is(again, err) {
						t.Fatal("repeated COPY Finish changed error identity", again, err)
					}
					assertFrames(true)
					if scope.Owner.CopyLease != nil || scope.Stage.Rows != nil {
						t.Fatal("COPY child retained stream authority or private rows")
					}
					for name := range core.PayloadOf(s).PreparedSQL.Bindings() {
						t.Fatal("COPY child retained a registered portal", name)
					}
				})
			}
		}
	}
}
