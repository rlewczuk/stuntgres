package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIndexMaintenanceUtilityQueries(t *testing.T) {
	for _, q := range []string{"ALTER INDEX ix RENAME TO fresh", "REINDEX INDEX ix", "REINDEX TABLE t", "DROP INDEX ix"} {
		for _, prepared := range []bool{false, true} {
			for _, plain := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/prepared=%t/plain=%t", q, prepared, plain), func(t *testing.T) {
					indexMaintenanceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
						run := func(s fixture.Step) { s.NonContext = plain; fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
						run(fixture.Exec("CREATE TABLE t(n INT)", 0))
						run(fixture.Exec("CREATE INDEX ix ON t(n)", 0))
						// Query utilities have a real, closable empty cursor and empty descriptors,
						// not fabricated SELECT columns or a nil successful result.
						s := fixture.Select(q, []string{}, []string{}, nil)
						if prepared {
							s = fixture.Prepared(s)
						}
						run(s)
						missing := "DROP INDEX missing"
						s = fixture.ImplicitError(missing, fixture.Diagnostic{Code: "42704", Message: `index "missing" does not exist`})
						s.SelectRows = true
						s.ErrorStage = "execute"
						if prepared {
							s = fixture.Prepared(s)
						}
						run(s)
					})
				})
			}
		}
	}
}

func TestReferenceIndexMaintenanceEmptyAndRepeatedKeys(t *testing.T) {
	for _, seed := range []string{"", "INSERT INTO t VALUES(NULL),(NULL)", "INSERT INTO t VALUES(1),(1),(2)"} {
		t.Run(seed, func(t *testing.T) {
			indexMaintenanceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE t(n INT)", 0))
				keys := make([]string, 32)
				for i := range keys {
					keys[i] = "n"
				}
				run(fixture.Exec("CREATE INDEX ix ON t("+strings.Join(keys, ",")+")", 0))
				want := [][]any(nil)
				if seed != "" {
					if strings.Contains(seed, "NULL") {
						run(fixture.Exec(seed, 2))
						want = [][]any{{nil}, {nil}}
					} else {
						run(fixture.Exec(seed, 3))
						want = [][]any{{int64(1)}, {int64(1)}, {int64(2)}}
					}
				}
				for _, q := range []string{"REINDEX INDEX ix", "REINDEX TABLE t", "ALTER INDEX ix RENAME TO fresh", "REINDEX INDEX fresh"} {
					run(fixture.Prepared(fixture.Exec(q, 0)))
					run(fixture.Select("SELECT n FROM t ORDER BY n NULLS LAST", []string{"n"}, []string{"INT4"}, want))
				}
				if b.PG {
					vector := func(value string) string { return strings.TrimSpace(strings.Repeat(value+" ", 32)) }
					run(fixture.Select("SELECT indnatts,indnkeyatts,indkey::text,indoption::text,indclass::text,indcollation::text FROM pg_index WHERE indexrelid='fresh'::regclass", []string{"indnatts", "indnkeyatts", "indkey", "indoption", "indclass", "indcollation"}, []string{"INT2", "INT2", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(32), int64(32), vector("1"), vector("0"), vector("1978"), vector("0")}}))
				}
				run(fixture.Exec("DROP INDEX fresh", 0))
				run(fixture.Select("SELECT n FROM t ORDER BY n NULLS LAST", []string{"n"}, []string{"INT4"}, want))
			})
		})
	}
}

func TestReferenceIndexMaintenanceRetainedSnapshot(t *testing.T) {
	indexMaintenanceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(n INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
		run(fixture.Exec("CREATE INDEX ix ON t(n)", 0))
		if !b.PG {
			rows, err := b.DB.QueryContext(p.Ctx, "SELECT n FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			columns, err := rows.Columns()
			if err != nil || len(columns) != 1 || columns[0] != "n" {
				t.Fatal(columns, err)
			}
			types, err := rows.ColumnTypes()
			if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
				t.Fatal(types, err)
			}
			for _, q := range []string{"ALTER INDEX ix RENAME TO fresh", "REINDEX INDEX fresh", "REINDEX TABLE t", "DROP INDEX fresh"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("UPDATE t SET n=2", 1))
			indexMaintenanceRetainedRows(t, rows)
			if types[0].DatabaseTypeName() != "INT4" || columns[0] != "n" {
				t.Fatal("retained descriptors mutated")
			}
		} else {
			// pq streams; capture before DDL rather than claim native eager semantics.
			run(fixture.Select("SELECT n FROM t", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
			for _, q := range []string{"ALTER INDEX ix RENAME TO fresh", "REINDEX INDEX fresh", "REINDEX TABLE t", "DROP INDEX fresh"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("UPDATE t SET n=2", 1))
		}
		run(fixture.Select("SELECT n FROM t", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}}))
	})
}
