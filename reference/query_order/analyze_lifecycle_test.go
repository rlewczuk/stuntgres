package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceAnalyzeLifecycle(t *testing.T) {
	analyzeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { analyzeRun(t, p, b, b.DB, s) }
		run(fixture.Exec("CREATE TABLE analyze_life(a INT,b INT)", 0))
		analyzeInsert(t, p, b, b.DB, "analyze_life", 14, 2, 4)
		run(fixture.Exec("ANALYZE analyze_life", 0))
		saved := explainQuery(t, p, b, "EXPLAIN SELECT b FROM analyze_life WHERE b=1", false)
		text := strings.Join(saved, "\n")
		run(fixture.Exec("ALTER TABLE analyze_life RENAME TO analyze_renamed", 0))
		analyzeWantEstimate(t, p, b.DB, "analyze_renamed", "b=1", 14)
		run(fixture.Exec("ALTER TABLE analyze_renamed DROP COLUMN a", 0))
		analyzeWantEstimate(t, p, b.DB, "analyze_renamed", "b=1", 14)
		run(fixture.Exec("UPDATE analyze_renamed SET b=2 WHERE b=1", 14))
		analyzeWantEstimate(t, p, b.DB, "analyze_renamed", "b=1", 14)
		run(fixture.Exec("VACUUM analyze_renamed", 0))
		analyzeWantEstimate(t, p, b.DB, "analyze_renamed", "b=1", 14)
		// New type authority cannot consume the old integer representatives.
		run(fixture.Exec("ALTER TABLE analyze_renamed ALTER COLUMN b TYPE TEXT USING b::text", 0))
		run(fixture.Exec("ANALYZE analyze_renamed", 0))
		analyzeWantEstimate(t, p, b.DB, "analyze_renamed", "b='2'", 16)
		run(fixture.Select("SELECT b FROM analyze_renamed ORDER BY b NULLS LAST", []string{"b"}, []string{"TEXT"}, append(analyzeCountRows(16, "2"), analyzeCountRows(4, nil)...)))
		run(fixture.Exec("DROP TABLE analyze_renamed", 0))
		run(fixture.Exec("CREATE TABLE analyze_renamed(b TEXT)", 0))
		analyzeTuples(t, p, b, b.DB, "analyze_renamed", -1)
		run(fixture.Exec("INSERT INTO analyze_renamed VALUES('new'),('new'),('new')", 3))
		run(fixture.Exec("ANALYZE analyze_renamed", 0))
		analyzeWantEstimate(t, p, b.DB, "analyze_renamed", "b='new'", 3)
		if strings.Join(saved, "\n") != text {
			t.Fatal("retained old plan mutated")
		}
		if b.PG {
			return
		} // Native controls have no PostgreSQL SQL equivalent.
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		retained, err := b.DB.QueryContext(p.Ctx, "SELECT b FROM analyze_renamed ORDER BY b")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled restore=%v", err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("not SQL")); err == nil {
			t.Fatal("invalid restore succeeded")
		}
		analyzeWantEstimate(t, p, b.DB, "analyze_renamed", "b='new'", 3)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		analyzeTuples(t, p, b, b.DB, "analyze_renamed", -1)
		run(fixture.Select("SELECT b FROM analyze_renamed ORDER BY b", []string{"b"}, []string{"TEXT"}, analyzeCountRows(3, "new")))
		run(fixture.Exec("ANALYZE analyze_renamed", 0))
		analyzeWantEstimate(t, p, b.DB, "analyze_renamed", "b='new'", 3)
		count := 0
		for retained.Next() {
			var value string
			if err := retained.Scan(&value); err != nil {
				t.Fatal(err)
			}
			if value != "new" {
				t.Fatalf("old snapshot value=%q", value)
			}
			count++
		}
		if retained.Err() != nil || count != 3 {
			t.Fatalf("retained SELECT rows=%d error=%v", count, retained.Err())
		}
		if strings.Join(saved, "\n") != text {
			t.Fatal("native replacement mutated saved plan")
		}
	})
}
