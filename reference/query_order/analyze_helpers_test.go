package reference_test

import (
	"database/sql"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// ANALYZE tests deliberately inspect public estimates, not the separately owned
// pg_stats reader. PostgreSQL physical and native logical plans are not equal.
var analyzeRowsPattern = regexp.MustCompile(`(?:estimated rows=|\brows=)([0-9]+)`)

func analyzeScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func analyzeRun(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, step fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, db, b.Recorder, step)
}

func analyzeEstimate(t *testing.T, p *fixture.Pair, db fixture.Executor, query string) int64 {
	t.Helper()
	rows, err := db.QueryContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	return analyzePlanEstimate(t, explainTextRows(t, rows))
}

func analyzePlanEstimate(t *testing.T, lines []string) int64 {
	t.Helper()
	// The first node is the result cardinality on both renderers. Never compare
	// costs, physical methods, or the number of plan nodes across backends.
	for _, line := range lines {
		if match := analyzeRowsPattern.FindStringSubmatch(line); match != nil {
			n, err := strconv.ParseInt(match[1], 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			return n
		}
	}
	t.Fatalf("missing cardinality in plan: %v", lines)
	return 0
}

func analyzeWantEstimate(t *testing.T, p *fixture.Pair, db fixture.Executor, table, predicate string, want int64) {
	t.Helper()
	q := "EXPLAIN SELECT * FROM " + table
	if predicate != "" {
		q += " WHERE " + predicate
	}
	if got := analyzeEstimate(t, p, db, q); got != want {
		t.Errorf("%s: estimated rows=%d, want %d", q, got, want)
	}
}

func analyzeTuples(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, table string, want int64) {
	t.Helper()
	analyzeRun(t, p, b, db, fixture.Select("SELECT reltuples FROM pg_class WHERE oid='"+table+"'::regclass", []string{"reltuples"}, []string{"FLOAT4"}, [][]any{{float64(want)}}))
}

func analyzeInsert(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, table string, ones, twos, nulls int) {
	t.Helper()
	values := make([]string, 0, ones+twos+nulls)
	for range ones {
		values = append(values, "(1,1)")
	}
	for range twos {
		values = append(values, "(2,2)")
	}
	for range nulls {
		values = append(values, "(NULL,NULL)")
	}
	analyzeRun(t, p, b, db, fixture.Exec("INSERT INTO "+table+" VALUES "+strings.Join(values, ","), int64(len(values))))
}

func analyzeHeld(t *testing.T, p *fixture.Pair, db fixture.Executor, query string) fixture.HeldStatement {
	t.Helper()
	s, err := db.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return fixture.HeldStatement{Executor: db, Statement: s, SQL: query}
}

func analyzeConn(t *testing.T, p *fixture.Pair, b fixture.Backend) *sql.Conn {
	t.Helper()
	c, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func analyzeError(code, message string) fixture.Diagnostic {
	return fixture.ControlsReferenceDiagnostic(code, message)
}

func analyzeCountRows(n int, value any) [][]any {
	rows := make([][]any, n)
	for i := range rows {
		rows[i] = []any{value}
	}
	return rows
}
