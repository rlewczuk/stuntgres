package reference_test

import (
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func fdwAdvancedLive(t *testing.T, p *fixture.Pair, b fixture.Backend, r *fdwSQLReader, run func(fixture.Step)) [][]any {
	t.Helper()
	r.advanced = &fdwAdvancedState{}
	fdwLive(t, p, b, r, run)
	fdwSourceExec(t, p, b, r, run, "DELETE FROM fdw_source.data", 3)
	var values []string
	var want [][]any
	for i := 1; i <= 20; i++ {
		label, flag := "'common'", "true"
		var text any = "common"
		var boolean any = true
		if i > 14 {
			label, flag, text, boolean = "'rare'", "false", "rare", false
		}
		if i > 16 {
			label, flag, text, boolean = "NULL", "NULL", nil, nil
		}
		values = append(values, fmt.Sprintf("(%d,%s,%s)", i, label, flag))
		want = append(want, []any{int64(i), text, boolean})
	}
	fdwSourceExec(t, p, b, r, run, "INSERT INTO fdw_source.data VALUES "+strings.Join(values, ","), 20)
	return want
}

func fdwExplainText(t *testing.T, p *fixture.Pair, executor fixture.Executor, query string, args ...any) []string {
	t.Helper()
	rows, err := executor.QueryContext(p.Ctx, query, args...)
	if err != nil {
		t.Fatal(query, err)
	}
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) != 1 || columns[0].Name() != "QUERY PLAN" || columns[0].DatabaseTypeName() != "TEXT" {
		t.Fatal("EXPLAIN metadata", columns)
	}
	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(lines) == 0 {
		t.Fatal("empty EXPLAIN")
	}
	return lines
}

func fdwExplainRoutes(t *testing.T, p *fixture.Pair, c *sql.Conn, query string, after func([]string)) {
	t.Helper()
	lines := fdwExplainText(t, p, c, query)
	after(lines)
	for _, repeats := range []int{1, 2} {
		statement, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		after(lines) // Prepare itself is metadata-only and nonexecuting.
		for range repeats {
			after(fdwExplainText(t, p, fixture.HeldStatement{Executor: c, Statement: statement, SQL: query}, query))
		}
		if err := statement.Close(); err != nil {
			t.Fatal(err)
		}
	}
	result, err := c.ExecContext(p.Ctx, query)
	if err != nil {
		t.Fatal(query, err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 0 {
		t.Fatal("EXPLAIN affected count", affected, err)
	}
	after(lines)
}

func fdwAssertSource(t *testing.T, p *fixture.Pair, b fixture.Backend, r *fdwSQLReader, want [][]any) {
	t.Helper()
	fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data ORDER BY id", want))
}

func TestReferenceFDWExplainPlans(t *testing.T) {
	fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		want := fdwAdvancedLive(t, p, b, r, run)
		fdwExplainParameters(t, p, b, c, r, want, run)
		run(fixture.Exec("CREATE VIEW fdw_plan_view AS SELECT id,label,flag FROM fdw_data", 0))
		for _, source := range []string{"fdw_data", "(SELECT * FROM fdw_data) AS derived", "fdw_plan_view", "fdw_data a JOIN fdw_data z ON a.id=z.id"} {
			fdwExplainRoutes(t, p, c, "EXPLAIN (VERBOSE,COSTS false) SELECT * FROM "+source, func(lines []string) {
				text := strings.Join(lines, "\n")
				if !strings.Contains(text, "Foreign Scan") || !strings.Contains(text, "Remote SQL:") || !strings.Contains(text, "fdw_source") {
					t.Fatal(text)
				}
				if strings.Contains(text, "cost=") || strings.Contains(text, "Actual Rows") {
					t.Fatal("invented or hidden metrics", text)
				}
				if !b.PG && !strings.Contains(text, "chosen second") {
					t.Fatal("unstable actual path choice", text)
				}
				fdwAssertSource(t, p, b, r, want)
			})
		}
		fdwExplainRoutes(t, p, c, "EXPLAIN (VERBOSE,COSTS false) WITH q AS (SELECT * FROM fdw_data) SELECT * FROM q", func(lines []string) {
			if !strings.Contains(strings.Join(lines, "\n"), "Foreign Scan") {
				t.Fatal(lines)
			}
			fdwAssertSource(t, p, b, r, want)
		})
		text := strings.Join(fdwExplainText(t, p, c, "EXPLAIN SELECT * FROM fdw_data"), "\n")
		if strings.Contains(text, "Remote SQL:") {
			t.Fatal("nonverbose provider disclosed SQL", text)
		}
		if !b.PG && !strings.Contains(text, "total cost=20.00") {
			t.Fatal("not the genuinely cheapest provider path", text)
		}
		run(fdwRows("SELECT id,label,flag FROM fdw_data ORDER BY id", want))
		// Public presentation migrated from the internal renderer unit: the same
		// aliases, CASE, literal quoting, NULL and sort direction stay observable.
		text = strings.Join(fdwExplainText(t, p, c, `EXPLAIN (VERBOSE,COSTS false) SELECT CASE WHEN "odd alias".id=2 THEN 'it''s' ELSE NULL END FROM fdw_data AS "odd alias" WHERE "odd alias".id=2 ORDER BY "odd alias".id DESC`), "\n")
		text += "\n" + strings.Join(fdwExplainText(t, p, c, `EXPLAIN (VERBOSE,COSTS false) SELECT id FROM fdw_data AS "odd alias" ORDER BY "odd alias".id DESC`), "\n")
		alias := `"odd alias".id`
		if b.PG {
			alias = `"odd alias"`
		}
		for _, fragment := range []string{alias, "CASE", "'it''s'", "NULL", "DESC", "Output:", "public.fdw_data"} {
			if !strings.Contains(text, fragment) {
				t.Fatalf("missing %q: %s", fragment, text)
			}
		}
		// A scan-only registration genuinely omits optional properties.
		if !b.PG {
			run(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_no_explain HANDLER fdw_scan_only_handler", 0))
			run(fixture.Exec("CREATE SERVER fdw_no_explain_server FOREIGN DATA WRAPPER fdw_no_explain", 0))
			run(fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER fdw_no_explain_server", 0))
			run(fixture.Exec("CREATE FOREIGN TABLE fdw_no_explain_table(id integer,label text,flag boolean) SERVER fdw_no_explain_server OPTIONS(schema_name 'fdw_source',table_name 'data')", 0))
			text = strings.Join(fdwExplainText(t, p, c, "EXPLAIN (VERBOSE) SELECT * FROM fdw_no_explain_table"), "\n")
			if !strings.Contains(text, "Foreign Scan") || strings.Contains(text, "Remote SQL:") {
				t.Fatal(text)
			}
			run(fixture.Exec("DROP SERVER fdw_no_explain_server CASCADE", 0))
			run(fixture.Exec("DROP FOREIGN DATA WRAPPER fdw_no_explain", 0))
		}
	})
}

func TestReferenceFDWExplainNonexecution(t *testing.T) {
	fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		want := fdwAdvancedLive(t, p, b, r, run)
		run(fixture.Exec("CREATE SEQUENCE fdw_explain_sequence", 0))
		run(fixture.Exec("CREATE TABLE fdw_explain_sentinel(id integer DEFAULT nextval('fdw_explain_sequence'))", 0))
		run(fixture.Exec("ALTER FOREIGN TABLE fdw_data ALTER id SET DEFAULT nextval('fdw_explain_sequence')", 0))
		before := r.snapshot()
		transactions, modifies := r.writer.transactions, r.writer.modifies
		for _, child := range []string{
			"SELECT nextval('fdw_explain_sequence'),id FROM fdw_data",
			"INSERT INTO fdw_data DEFAULT VALUES RETURNING id",
			"UPDATE fdw_data SET label='changed' WHERE id=1 RETURNING id",
			"DELETE FROM fdw_data WHERE id=2 RETURNING id",
			"WITH w AS (INSERT INTO fdw_explain_sentinel VALUES(99) RETURNING id) SELECT * FROM w,fdw_data",
		} {
			fdwExplainRoutes(t, p, c, "EXPLAIN (VERBOSE,ANALYZE false,COSTS false) "+child, func(lines []string) {
				if strings.Contains(strings.Join(lines, "\n"), "Actual Rows") {
					t.Fatal(lines)
				}
				fdwAssertSource(t, p, b, r, want)
				run(fixture.Select("SELECT id FROM fdw_explain_sentinel ORDER BY id", []string{"id"}, []string{"INT4"}, nil))
				run(fixture.Select("SELECT last_value,is_called FROM fdw_explain_sequence", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
			})
		}
		if !b.PG {
			after := r.snapshot()
			if after.begins != before.begins || after.iterations != before.iterations || r.writer.transactions != transactions || r.writer.modifies != modifies {
				t.Fatal("plain EXPLAIN entered execution")
			}
			diagnostic := fdwDiagnostic("0A000", "EXPLAIN option ANALYZE is not supported")
			diagnostic.Position = fixture.Position(9)
			step := fixture.ErrorDiagnostic("EXPLAIN ANALYZE SELECT * FROM fdw_data", diagnostic)
			run(step)
		}
	})
}

var fdwRowsEstimate = regexp.MustCompile(`rows=([0-9]+)`)

func fdwEstimate(t *testing.T, lines []string) int64 {
	t.Helper()
	matches := fdwRowsEstimate.FindStringSubmatch(lines[0])
	if len(matches) != 2 {
		t.Fatal(lines)
	}
	n, err := strconv.ParseInt(matches[1], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
func fdwWantEstimate(t *testing.T, p *fixture.Pair, c *sql.Conn, predicate string, want int64) {
	t.Helper()
	lines := fdwExplainText(t, p, c, "EXPLAIN SELECT * FROM fdw_data WHERE "+predicate)
	if got := fdwEstimate(t, lines); got != want {
		t.Fatalf("%s estimate=%d want=%d: %v", predicate, got, want, lines)
	}
}

func fdwExplainParameters(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, want [][]any, run func(fixture.Step)) {
	t.Helper()
	query := "EXPLAIN (VERBOSE,COSTS false) SELECT id,label FROM fdw_data WHERE id=$1"
	statement, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	for _, executor := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: statement, SQL: query}} {
		for _, argument := range []int64{2, 7} {
			lines := fdwExplainText(t, p, executor, query, argument)
			if !strings.Contains(strings.Join(lines, "\n"), "Foreign Scan") {
				t.Fatal(lines)
			}
			fdwAssertSource(t, p, b, r, want)
			if !b.PG {
				r.advanced.mu.Lock()
				request := r.advanced.scanRequests[len(r.advanced.scanRequests)-1]
				r.advanced.mu.Unlock()
				if len(request.Parameters.Values) != 1 || request.Parameters.Values[0] != argument {
					t.Fatal("actual typed planning arguments lost", request.Parameters)
				}
			}
		}
	}
	for _, prepared := range []bool{false, true} {
		diagnostic := fdwDiagnostic("22P02", `invalid input syntax for type integer: "not an integer"`)
		diagnostic.Context = "unnamed portal parameter $1 = '...'"
		step := fixture.ErrorDiagnostic(query, diagnostic, "not an integer")
		step.SelectRows = true
		if prepared {
			step = fixture.Prepared(step)
		}
		run(step)
		fdwAssertSource(t, p, b, r, want)
	}
}
