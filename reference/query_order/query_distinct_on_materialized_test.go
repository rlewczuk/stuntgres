package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

type queryDistinctOnMaterializedWitness struct {
	name      string
	populated bool
	step      fixture.Step
}

func TestReferenceQueryDistinctOnMaterializedDefinitions(t *testing.T) {
	queryDistinctOnScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		queryDistinctOnEvents(run)
		run(fixture.Exec("CREATE TABLE only_key(k INT)", 0))
		run(fixture.Exec("INSERT INTO only_key VALUES(1)", 1))
		var witnesses []queryDistinctOnMaterializedWitness
		for _, shape := range []struct {
			name, query string
			rows        [][]any
		}{
			{"hidden", "SELECT DISTINCT ON(k) id,payload FROM events ORDER BY k,stamp DESC,id DESC", queryDistinctOnHidden().Rows},
			{"scalar", "SELECT DISTINCT ON((SELECT k FROM only_key)) id,payload FROM events ORDER BY (SELECT only_key.k FROM only_key),id DESC", [][]any{{int64(7), "newnull"}}},
		} {
			for _, prepared := range []bool{false, true} {
				for _, noData := range []bool{false, true} {
					name := "mv_on_" + shape.name + map[bool]string{false: "_direct", true: "_prepared"}[prepared] + map[bool]string{false: "", true: "_no"}[noData]
					count := int64(len(shape.rows))
					suffix := ""
					if noData {
						count = 0
						suffix = " WITH NO DATA"
					}
					create := fixture.Exec("CREATE MATERIALIZED VIEW "+name+" AS "+shape.query+suffix, count)
					create.Prepared = prepared
					run(create)
					selected := fixture.Select("SELECT id,payload FROM "+name+" ORDER BY id", []string{"id", "payload"}, []string{"INT4", "TEXT"}, shape.rows)
					selected.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						cols, err := rows.ColumnTypes()
						if err != nil {
							t.Fatal(err)
						}
						fixture.TableDDLColumnTypes(t, b.PG, cols, selected.Columns, selected.Types, []bool{true, true})
					}
					witnesses = append(witnesses, queryDistinctOnMaterializedWitness{name, !noData, selected})
					fixture.MaterializedViewPopulation(run, name, !noData)
					if noData {
						fixture.QueryDistinctBoth(run, fixture.MaterializedViewUnpopulated(name))
					} else {
						fixture.QueryDistinctBoth(run, selected)
						queryDistinctOnHeld(t, p, b, c, selected)
					}
					// Execute the actual returned definition, without pinning pretty syntax.
					var definition string
					if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef('"+name+"'::regclass)").Scan(&definition); err != nil {
						t.Fatal(err)
					}
					replayName := name + "_reconstructed"
					run(fixture.Exec("CREATE MATERIALIZED VIEW "+replayName+" AS "+definition, int64(len(shape.rows))))
					replay := selected
					replay.Query = "SELECT id,payload FROM " + replayName + " ORDER BY id"
					fixture.QueryDistinctBoth(run, replay)
					witnesses = append(witnesses, queryDistinctOnMaterializedWitness{replayName, true, replay})
				}
			}
		}
		// Materialized rows stay stale even when the live ON definition has a new winner.
		run(fixture.Exec("INSERT INTO events VALUES(8,NULL,'2025-01-05','freshnull')", 1))
		fixture.QueryDistinctBoth(run, fixture.Select("SELECT DISTINCT ON(k) id,payload FROM events ORDER BY k,stamp DESC,id DESC", []string{"id", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "new1b"}, {int64(5), "new2"}, {int64(8), "freshnull"}}))
		for _, w := range witnesses {
			fixture.MaterializedViewPopulation(run, w.name, w.populated)
			if w.populated {
				fixture.QueryDistinctBoth(run, w.step)
			} else {
				fixture.QueryDistinctBoth(run, fixture.MaterializedViewUnpopulated(w.name))
			}
		}
		if !b.PG {
			queryDistinctOnMaterializedNativeReplay(t, p, witnesses)
		}
	})
}

func TestReferenceQueryDistinctOnMaterializedPolicies(t *testing.T) {
	queryDistinctOnScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		queryDistinctOnEvents(run)
		run(fixture.Exec("CREATE MATERIALIZED VIEW mv_on_sentinel AS SELECT DISTINCT ON(1) 17 AS sentinel FROM events ORDER BY 1", 1))
		sentinel := fixture.Select("SELECT sentinel FROM mv_on_sentinel", []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(17)}})
		for _, suffix := range []string{"", " WITH NO DATA"} {
			parameter := fixture.Prepared(fixture.ErrorDiagnostic("CREATE MATERIALIZED VIEW mv_on_parameter AS SELECT DISTINCT ON($1::integer) id FROM events"+suffix, fixture.ControlsReferenceDiagnostic("0A000", "materialized views may not be defined using bound parameters"), 7))
			parameter.ErrorStage = "prepare"
			run(parameter)
			fixture.QueryTableCreateAbsent(t, run, b.PG, "mv_on_parameter")
			run(sentinel)
		}
		failing := "SELECT DISTINCT ON(k) 1/(id-1) AS payload FROM events ORDER BY k,id DESC"
		for _, prepared := range []bool{false, true} {
			failure := fixture.ImplicitError("CREATE MATERIALIZED VIEW mv_on_failed AS "+failing, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
			failure.Prepared = prepared
			failure.ErrorStage = "execute"
			run(failure)
			fixture.QueryTableCreateAbsent(t, run, b.PG, "mv_on_failed")
			run(sentinel)
			fixture.MaterializedViewPopulation(run, "mv_on_sentinel", true)
		}
		run(fixture.Exec("CREATE MATERIALIZED VIEW mv_on_deferred AS "+failing+" WITH NO DATA", 0))
		fixture.QueryDistinctBoth(run, fixture.MaterializedViewUnpopulated("mv_on_deferred"))
		fixture.MaterializedViewPopulation(run, "mv_on_deferred", false)
		run(sentinel)
	})
}

func queryDistinctOnMaterializedNativeReplay(t *testing.T, p *fixture.Pair, witnesses []queryDistinctOnMaterializedWitness) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	fresh := fixture.NewFixture(t, false, true)
	if err := stuntgres.Restore(p.Ctx, fresh.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, fresh.Memory, fresh.MemoryRecord, s) }
	check := func() {
		for _, w := range witnesses {
			fixture.MaterializedViewPopulation(run, w.name, w.populated)
			if w.populated {
				fixture.QueryDistinctBoth(run, w.step)
			} else {
				fixture.QueryDistinctBoth(run, fixture.MaterializedViewUnpopulated(w.name))
			}
		}
	}
	check()
	retained, err := fresh.Native.QueryContext(p.Ctx, witnesses[0].step.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	invalid, want := queryDistinctOnMarked(dump.String()+"/* λ */ CREATE MATERIALIZED VIEW broken_on_mv AS SELECT DISTINCT ON(|k) id FROM events ORDER BY id WITH NO DATA;", "42P10", "SELECT DISTINCT ON expressions must match initial ORDER BY expressions")
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, fresh.Native, bytes.NewBufferString(invalid)), &want)
	check()
	canceled, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(canceled, fresh.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	check()
	if err := stuntgres.Reset(p.Ctx, fresh.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, fresh.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	first := witnesses[0].step
	fixture.StringCoreRetainedRows(t, retained, nil, first.Columns, first.Types, first.Rows, first.Bag)
	check()
	// PG consumes the emitted definitions, not native snapshot comment records.
	// Its restored materialized definitions intentionally remain WITH NO DATA.
	if p.Postgres != nil {
		tx, err := p.Postgres.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("emitted materialized ON definition replay", err)
		}
		for _, w := range witnesses {
			if _, err := tx.ExecContext(p.Ctx, "SAVEPOINT unpopulated_on"); err != nil {
				t.Fatal(err)
			}
			rows, err := tx.QueryContext(p.Ctx, "SELECT * FROM "+w.name)
			if rows != nil {
				rows.Close()
				t.Fatal("unpopulated emitted definition returned rows", w.name)
			}
			expected := fixture.ControlsReferenceDiagnostic("55000", "materialized view \""+w.name+"\" has not been populated")
			expected.Hint = "Use the REFRESH MATERIALIZED VIEW command."
			fixture.AssertDiagnostic(t, err, &expected)
			if _, err := tx.ExecContext(p.Ctx, "ROLLBACK TO SAVEPOINT unpopulated_on"); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.ExecContext(p.Ctx, "RELEASE SAVEPOINT unpopulated_on"); err != nil {
				t.Fatal(err)
			}
		}
	}
}
