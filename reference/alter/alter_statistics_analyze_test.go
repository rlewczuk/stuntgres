package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func statisticsInsert(n int) string {
	values := make([]string, n)
	for i := range values {
		values[i] = fmt.Sprintf("(%d,%d)", i+1, i+1)
	}
	return "INSERT INTO statistics_t VALUES" + strings.Join(values, ",")
}
func statisticsData(n int) fixture.Step {
	rows := make([][]any, n)
	for i := range rows {
		rows[i] = []any{int64(i + 1)}
	}
	return fixture.Select("SELECT a FROM statistics_t ORDER BY a", []string{"a"}, []string{"INT4"}, rows)
}

var statisticsRowsPattern = regexp.MustCompile(`rows=([0-9]+)`)

func statisticsEstimate(t *testing.T, p *fixture.Pair, q interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}, query string, want int64) []string {
	t.Helper()
	rows, err := q.QueryContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
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
	match := statisticsRowsPattern.FindStringSubmatch(strings.Join(lines, "\n"))
	if len(match) != 2 {
		t.Fatal(lines)
	}
	got, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil || got != want {
		t.Fatalf("estimate=%d want %d: %v (%v)", got, want, lines, err)
	}
	return lines
}

func TestReferenceAlterStatisticsAnalyze(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE statistics_t(a integer,b integer)", 0))
	p.Step(t, fixture.Exec(statisticsInsert(20), 20))
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("ANALYZE statistics_t", 0))
			statisticsEstimate(t, p, b.DB, "EXPLAIN SELECT a FROM statistics_t WHERE a=1", 1)
			run(fixture.Exec("ALTER TABLE statistics_t ALTER a SET(n_distinct=2)", 0))
			statisticsEstimate(t, p, b.DB, "EXPLAIN SELECT a FROM statistics_t WHERE a=1", 1)
			run(fixture.Exec("ANALYZE statistics_t", 0))
			statisticsEstimate(t, p, b.DB, "EXPLAIN SELECT a FROM statistics_t WHERE a=1", 10)
			run(statisticsData(20))
			run(fixture.Select("SELECT a FROM statistics_t WHERE a=1 ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
			for _, tc := range []struct {
				value    string
				estimate int64
			}{{"2.5", 10}, {"-0.5", 2}, {"0", 1}} {
				run(fixture.Exec("ALTER TABLE statistics_t ALTER a SET(n_distinct="+tc.value+")", 0))
				run(statisticsMetadata("statistics_t", []any{"a", nil, "{n_distinct=" + tc.value + "}"}, []any{"b", nil, nil}))
				run(fixture.Exec("ANALYZE statistics_t(a)", 0))
				statisticsEstimate(t, p, b.DB, "EXPLAIN SELECT a FROM statistics_t WHERE a=1", tc.estimate)
			}
			run(fixture.Exec("ALTER TABLE statistics_t ALTER a SET(n_distinct=-0.5)", 0))
			run(fixture.Exec("ANALYZE statistics_t(a)", 0))
			values := make([]string, 20)
			for i := range values {
				values[i] = fmt.Sprintf("(%d,%d)", i+21, i+21)
			}
			run(fixture.Exec("INSERT INTO statistics_t VALUES"+strings.Join(values, ","), 20))
			run(fixture.Exec("ANALYZE statistics_t(b)", 0))
			statisticsEstimate(t, p, b.DB, "EXPLAIN SELECT a FROM statistics_t WHERE a=1", 2)
			run(fixture.Exec("ALTER TABLE statistics_t ALTER a SET STATISTICS 0, ALTER a SET(n_distinct=2)", 0))
			run(fixture.Exec("ANALYZE statistics_t", 0))
			statisticsEstimate(t, p, b.DB, "EXPLAIN SELECT a FROM statistics_t WHERE a=1", 2)
			run(fixture.Exec("ALTER TABLE statistics_t ALTER a SET STATISTICS DEFAULT, ALTER a RESET(n_distinct)", 0))
			run(fixture.Exec("ANALYZE statistics_t", 0))
			statisticsEstimate(t, p, b.DB, "EXPLAIN SELECT a FROM statistics_t WHERE a=1", 1)
			run(statisticsData(40))
		})
	}
	// Local and inherited estimates have independent overrides and populations.
	for _, q := range []string{"CREATE TABLE statistics_p(a integer)", "CREATE TABLE statistics_c() INHERITS(statistics_p)", "ALTER TABLE statistics_p ALTER a SET(n_distinct=2,n_distinct_inherited=4)"} {
		p.Step(t, fixture.Exec(q, 0))
	}
	p.Step(t, fixture.Exec("INSERT INTO statistics_p VALUES(1),(2),(3),(4),(5),(6),(7),(8)", 8))
	p.Step(t, fixture.Exec("INSERT INTO statistics_c VALUES(9),(10),(11),(12),(13),(14),(15),(16)", 8))
	p.Step(t, fixture.Exec("ANALYZE statistics_p", 0))
	for _, b := range p.Backends() {
		statisticsEstimate(t, p, b.DB, "EXPLAIN SELECT a FROM ONLY statistics_p WHERE a=1", 4)
	}
}

func TestReferenceAlterStatisticsPrepared(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE statistics_t(a integer NOT NULL,b integer)", 0))
	p.Step(t, fixture.Exec(statisticsInsert(20), 20))
	p.Step(t, fixture.Exec("ANALYZE statistics_t", 0))
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			meta := statisticsMetadata("statistics_t", []any{"a", nil, nil}, []any{"b", nil, nil})
			catalogStmt, err := c.PrepareContext(p.Ctx, meta.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer catalogStmt.Close()
			explainQuery := "EXPLAIN SELECT a FROM statistics_t WHERE a=1"
			explain, err := c.PrepareContext(p.Ctx, explainQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer explain.Close()
			selectQuery := "SELECT a FROM statistics_t ORDER BY a"
			selected, err := c.PrepareContext(p.Ctx, selectQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer selected.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			read := func(s fixture.Step) {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: catalogStmt, SQL: meta.Query}, b.Recorder, s)
			}
			read(meta)
			old := statisticsEstimate(t, p, fixture.HeldStatement{Executor: c, Statement: explain, SQL: explainQuery}, explainQuery, 1)
			saved := strings.Join(old, "\n")
			utility := "ALTER TABLE statistics_t ALTER a SET STATISTICS 3, ALTER a SET(n_distinct=2)"
			u, err := c.PrepareContext(p.Ctx, utility)
			if err != nil {
				t.Fatal(err)
			}
			defer u.Close()
			read(meta) // Preparing utilities must not mutate catalogs.
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: u, SQL: utility}, b.Recorder, fixture.Exec(utility, 0))
			read(statisticsMetadata("statistics_t", []any{"a", int64(3), "{n_distinct=2}"}, []any{"b", nil, nil}))
			statisticsEstimate(t, p, fixture.HeldStatement{Executor: c, Statement: explain, SQL: explainQuery}, explainQuery, 1)
			run(fixture.Exec("ANALYZE statistics_t", 0))
			statisticsEstimate(t, p, fixture.HeldStatement{Executor: c, Statement: explain, SQL: explainQuery}, explainQuery, 10)
			data := statisticsData(20)
			data.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				ct, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				fixture.TableDDLColumnTypes(t, b.PG, ct, []string{"a"}, []string{"INT4"}, []bool{false})
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: selected, SQL: selectQuery}, b.Recorder, data)
			if strings.Join(old, "\n") != saved {
				t.Fatal("retained EXPLAIN changed")
			}
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("ALTER TABLE statistics_t ALTER a SET STATISTICS 7", 0))
			run(fixture.Exec("ROLLBACK", 0))
			read(statisticsMetadata("statistics_t", []any{"a", int64(3), "{n_distinct=2}"}, []any{"b", nil, nil}))
		})
	}
}
