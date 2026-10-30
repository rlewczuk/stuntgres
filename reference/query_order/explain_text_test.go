package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Plans have independent structural expectations, not bytewise backend parity:
// PostgreSQL is free to choose physical methods and eliminate redundant sorts.
func explainPlanRows(t *testing.T, rows *sql.Rows) []string {
	t.Helper()
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"QUERY PLAN"}) {
		t.Fatalf("columns = %v, %v; want [QUERY PLAN]", columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 {
		t.Fatalf("column types = %v, %v", types, err)
	}
	if types[0].DatabaseTypeName() != "TEXT" || types[0].ScanType() != reflect.TypeFor[string]() {
		t.Errorf("plan type = %s/%v; want TEXT/string", types[0].DatabaseTypeName(), types[0].ScanType())
	}
	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		if strings.ContainsAny(line, "\r\n") {
			t.Errorf("plan row contains a line break: %q", line)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(lines) == 0 || strings.TrimSpace(strings.Join(lines, "")) == "" {
		t.Fatal("EXPLAIN returned no plan text")
	}
	return lines
}

func explainTextRows(t *testing.T, rows *sql.Rows) []string {
	t.Helper()
	lines := explainPlanRows(t, rows)
	for _, forbidden := range []string{"actual time=", "actual rows=", "Execution Time:", "Planning Time:", "loops="} {
		if strings.Contains(strings.Join(lines, "\n"), forbidden) {
			t.Errorf("nonexecuting plan contains %q: %v", forbidden, lines)
		}
	}
	return lines
}

func explainQuery(t *testing.T, p *fixture.Pair, b fixture.Backend, query string, prepared bool, args ...any) []string {
	t.Helper()
	if prepared {
		stmt, err := b.DB.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		rows, err := stmt.QueryContext(p.Ctx, args...)
		if err != nil {
			t.Fatal(err)
		}
		return explainTextRows(t, rows)
	}
	rows, err := b.DB.QueryContext(p.Ctx, query, args...)
	if err != nil {
		t.Fatal(err)
	}
	return explainTextRows(t, rows)
}

func explainScanStructure(t *testing.T, b fixture.Backend, lines []string) {
	t.Helper()
	text := strings.Join(lines, "\n")
	for _, detail := range []string{"explain_t", "id", "2"} {
		if !strings.Contains(text, detail) {
			t.Errorf("plan omits %q: %s", detail, text)
		}
	}
	if b.PG {
		// Filter, index condition, or another legitimate physical restriction
		// can represent id=2. ORDER BY id needs no sort when id is constant.
		if !strings.Contains(text, "=") || !strings.Contains(text, "rows=") {
			t.Errorf("plan lacks restriction/estimate: %s", text)
		}
		return
	}
	previous := -1
	for _, stage := range []string{"Project", "Sort", "Filter", "Logical Scan"} {
		index := strings.Index(text, stage)
		if index < 0 || index <= previous {
			t.Errorf("expected Project -> Sort -> Filter -> Logical Scan; missing/misordered %s: %s", stage, text)
		}
		previous = index
	}
	for _, detail := range []string{"DESC", "estimated rows=", "logical cost=", "Estimates: logical heuristics; not execution measurements"} {
		if !strings.Contains(text, detail) {
			t.Errorf("native plan omits %q: %s", detail, text)
		}
	}
	for _, line := range lines {
		if strings.Contains(line, "logical cost=") && !strings.Contains(line, "estimated rows=") {
			t.Errorf("unlabelled row estimate: %q", line)
		}
	}
}

func TestReferenceExplainText(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE explain_t(id integer, v text)", 0))
			run(fixture.Exec("INSERT INTO explain_t VALUES (1,'one'),(2,'two'),(3,'three')", 3))
			const query = "EXPLAIN SELECT id FROM explain_t WHERE id=2 ORDER BY id DESC"
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
					explainScanStructure(t, b, explainQuery(t, p, b, query, prepared))
					s := fixture.Exec(query, 0)
					s.Prepared = prepared
					run(s)
					explainQuery(t, p, b, "EXPLAIN SELECT", prepared)
					for _, parenthesized := range []string{
						"EXPLAIN (SELECT 1)",
						"EXPLAIN ((SELECT 1))",
						"EXPLAIN (VALUES (1),(2))",
						"EXPLAIN (COSTS OFF) (SELECT 1)",
						"EXPLAIN VERBOSE (SELECT 1)",
					} {
						t.Run(parenthesized, func(t *testing.T) {
							explainQuery(t, p, b, parenthesized, prepared)
						})
					}
				})
			}
			run(fixture.Select("SELECT id,v FROM explain_t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "two"}, {int64(3), "three"}}))
		})
	}
}
