package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func fdwPopulation(run func(fixture.Step), rows float64) {
	run(fixture.Select("SELECT reltuples FROM pg_class WHERE relname='fdw_data'", []string{"reltuples"}, []string{"FLOAT4"}, [][]any{{rows}}))
}

func TestReferenceFDWAnalyzeDistributions(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		want := fdwAdvancedLive(t, p, b, r, run)
		fdwPopulation(run, -1)
		before := r.snapshot()
		run(fixture.Exec("ANALYZE", 0))
		fdwPopulation(run, -1)
		if !b.PG && r.snapshot() != before {
			t.Fatal("bare ANALYZE sampled foreign table")
		}
		for _, prepared := range []bool{false, true} {
			step := fixture.Exec("ANALYZE fdw_data", 0)
			if prepared {
				step = fixture.Prepared(step)
			}
			run(step)
			fdwPopulation(run, 20)
			for _, expected := range []struct {
				predicate string
				rows      int64
			}{{"label='common'", 14}, {"label='rare'", 2}, {"label IS NULL", 4}, {"flag=true", 14}} {
				fdwWantEstimate(t, p, c, expected.predicate, expected.rows)
			}
			fdwAssertSource(t, p, b, r, want)
		}
		analyzeQuery := "ANALYZE fdw_data"
		before = r.snapshot()
		analyzeStatement, err := c.PrepareContext(p.Ctx, analyzeQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer analyzeStatement.Close()
		if !b.PG && r.snapshot() != before {
			t.Fatal("Prepare sampled foreign data")
		}
		for range 2 {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: analyzeStatement, SQL: analyzeQuery}, b.Recorder, fixture.Exec(analyzeQuery, 0))
			fdwPopulation(run, 20)
			fdwWantEstimate(t, p, c, "label='common'", 14)
			fdwAssertSource(t, p, b, r, want)
		}
		q := "EXPLAIN SELECT * FROM fdw_data WHERE label='rare'"
		statement, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer statement.Close()
		held := fixture.HeldStatement{Executor: c, Statement: statement, SQL: q}
		old := fdwExplainText(t, p, held, q)
		saved := strings.Join(old, "\n")
		fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.data SET label='rare',flag=false WHERE label='common'", 14)
		fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.data SET flag=true WHERE label IS NULL", 4)
		fdwWantEstimate(t, p, c, "label='rare'", 2)
		run(fixture.Exec("ANALYZE fdw_data(label)", 0))
		if got := fdwEstimate(t, fdwExplainText(t, p, held, q)); got != 16 {
			t.Fatalf("held selected-column estimate=%d want16", got)
		}
		fdwWantEstimate(t, p, c, "flag=true", 14)
		run(fixture.Exec("ANALYZE fdw_data(flag)", 0))
		fdwWantEstimate(t, p, c, "flag=true", 4)
		if strings.Join(old, "\n") != saved {
			t.Fatal("old plan mutated")
		}
		run(fixture.Exec("ALTER FOREIGN TABLE fdw_data ALTER label SET STATISTICS 0", 0))
		fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.data SET label='common'", 20)
		run(fixture.Exec("ANALYZE fdw_data", 0))
		fdwWantEstimate(t, p, c, "label='rare'", 16)
		run(fixture.Exec("ALTER FOREIGN TABLE fdw_data ALTER label SET STATISTICS 100", 0))
		fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.data SET label=NULL", 20)
		run(fixture.Exec("ANALYZE fdw_data", 0))
		fdwWantEstimate(t, p, c, "label IS NULL", 20)
		fdwWantEstimate(t, p, c, "id=7", 1)
		// Foreign spelling consumes the same column configuration as ordinary
		// ALTER TABLE: an explicit distinct estimate wins, and RESET restores
		// measured singleton statistics after the next collection.
		run(fixture.Prepared(fixture.Exec("ALTER FOREIGN TABLE fdw_data ALTER id SET (n_distinct=5)", 0)))
		run(fixture.Exec("ANALYZE fdw_data(id)", 0))
		fdwWantEstimate(t, p, c, "id=7", 4)
		run(fixture.Exec("ALTER FOREIGN TABLE fdw_data ALTER id SET (n_distinct=0)", 0))
		run(fixture.Prepared(fixture.Exec("ANALYZE fdw_data(id)", 0)))
		fdwWantEstimate(t, p, c, "id=7", 1)
		run(fixture.Prepared(fixture.Exec("ALTER FOREIGN TABLE fdw_data ALTER id SET (n_distinct=5)", 0)))
		run(fixture.Exec("ANALYZE fdw_data(id)", 0))
		fdwWantEstimate(t, p, c, "id=7", 4)
		run(fixture.Exec("ALTER FOREIGN TABLE fdw_data ALTER id RESET (n_distinct)", 0))
		run(fixture.Prepared(fixture.Exec("ANALYZE fdw_data(id)", 0)))
		fdwWantEstimate(t, p, c, "id=7", 1)
		run(fixture.ErrorDiagnostic("ALTER FOREIGN TABLE fdw_data ALTER id SET STATISTICS -2", fdwDiagnostic("22023", "statistics target -2 is too low")))
		fdwSourceExec(t, p, b, r, run, "DELETE FROM fdw_source.data", 20)
		run(fixture.Prepared(fixture.Exec("ANALYZE fdw_data", 0)))
		fdwPopulation(run, 0)
		run(fdwRows("SELECT id,label,flag FROM fdw_data ORDER BY id", nil))
		run(fixture.Exec("CREATE FOREIGN TABLE fdw_empty_analyze(id integer,label text,flag boolean) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'data')", 0))
		run(fixture.Exec("ANALYZE fdw_empty_analyze", 0))
		run(fixture.Select("SELECT reltuples FROM pg_class WHERE relname='fdw_empty_analyze'", []string{"reltuples"}, []string{"FLOAT4"}, [][]any{{float64(0)}}))
		fdwAnalyzeSubset(t, p, b, c, r, run)
	})
}

func fdwAnalyzeSubset(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
	t.Helper()
	var values []string
	for i := 1; i <= 200; i++ {
		label := "'common'"
		if i > 140 {
			label = "'rare'"
		}
		if i > 160 {
			label = "NULL"
		}
		values = append(values, fmt.Sprintf("(%d,%s,true)", i, label))
	}
	fdwSourceExec(t, p, b, r, run, "INSERT INTO fdw_source.data VALUES "+strings.Join(values, ","), 200)
	r.advanced.mu.Lock()
	r.advanced.limit = 20
	r.advanced.mu.Unlock()
	run(fixture.Exec("ANALYZE fdw_data", 0))
	fdwPopulation(run, 200)
	run(fixture.Select("SELECT count(*) FROM fdw_data", []string{"count"}, []string{"INT8"}, [][]any{{int64(200)}}))
	if b.PG {
		fdwWantEstimate(t, p, c, "label='common'", 140)
		fdwWantEstimate(t, p, c, "label='rare'", 20)
		fdwWantEstimate(t, p, c, "label IS NULL", 40)
		return
	}
	r.advanced.mu.Lock()
	sample := r.advanced.sample
	r.advanced.mu.Unlock()
	if len(sample.Rows) != 20 || sample.TotalRows != 200 {
		t.Fatal("sample/population conflated", sample)
	}
	seen := make(map[int64]bool)
	var common, nulls int64
	for _, row := range sample.Rows {
		id, ok := row[0].(int64)
		if !ok || id < 1 || id > 200 || seen[id] {
			t.Fatal("sample not a source subset", row)
		}
		seen[id] = true
		var label any = "common"
		if id > 140 {
			label = "rare"
		}
		if id > 160 {
			label = nil
		}
		if row[1] != label || row[2] != true {
			t.Fatal("sample contains fabricated data", row)
		}
		if label == "common" {
			common++
		}
		if label == nil {
			nulls++
		}
	}
	if common > 1 {
		fdwWantEstimate(t, p, c, "label='common'", common*10)
	}
	fdwWantEstimate(t, p, c, "label IS NULL", nulls*10)
	state := r.snapshot()
	if state.openPools != 0 || state.openRows != 0 {
		t.Fatal("sample resources retained", state)
	}
}
