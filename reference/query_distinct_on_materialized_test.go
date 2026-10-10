package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"testing"
)

type queryDistinctOnMaterializedWitness struct {
	name      string
	populated bool
	step      referenceStep
}

func TestReferenceQueryDistinctOnMaterializedDefinitions(t *testing.T) {
	queryDistinctOnScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		queryDistinctOnEvents(run)
		run(referenceExec("CREATE TABLE only_key(k INT)", 0))
		run(referenceExec("INSERT INTO only_key VALUES(1)", 1))
		var witnesses []queryDistinctOnMaterializedWitness
		for _, shape := range []struct {
			name, query string
			rows        [][]any
		}{
			{"hidden", "SELECT DISTINCT ON(k) id,payload FROM events ORDER BY k,stamp DESC,id DESC", queryDistinctOnHidden().rows},
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
					create := referenceExec("CREATE MATERIALIZED VIEW "+name+" AS "+shape.query+suffix, count)
					create.prepared = prepared
					run(create)
					selected := referenceSelect("SELECT id,payload FROM "+name+" ORDER BY id", []string{"id", "payload"}, []string{"INT4", "TEXT"}, shape.rows)
					selected.checkMetadata = func(t *testing.T, rows *sql.Rows) {
						cols, err := rows.ColumnTypes()
						if err != nil {
							t.Fatal(err)
						}
						referenceTableDDLColumnTypes(t, b.pg, cols, selected.columns, selected.types, []bool{true, true})
					}
					witnesses = append(witnesses, queryDistinctOnMaterializedWitness{name, !noData, selected})
					materializedViewPopulation(run, name, !noData)
					if noData {
						queryDistinctBoth(run, materializedViewUnpopulated(name))
					} else {
						queryDistinctBoth(run, selected)
						queryDistinctOnHeld(t, p, b, c, selected)
					}
					// Execute the actual returned definition, without pinning pretty syntax.
					var definition string
					if err := c.QueryRowContext(p.ctx, "SELECT pg_get_viewdef('"+name+"'::regclass)").Scan(&definition); err != nil {
						t.Fatal(err)
					}
					replayName := name + "_reconstructed"
					run(referenceExec("CREATE MATERIALIZED VIEW "+replayName+" AS "+definition, int64(len(shape.rows))))
					replay := selected
					replay.query = "SELECT id,payload FROM " + replayName + " ORDER BY id"
					queryDistinctBoth(run, replay)
					witnesses = append(witnesses, queryDistinctOnMaterializedWitness{replayName, true, replay})
				}
			}
		}
		// Materialized rows stay stale even when the live ON definition has a new winner.
		run(referenceExec("INSERT INTO events VALUES(8,NULL,'2025-01-05','freshnull')", 1))
		queryDistinctBoth(run, referenceSelect("SELECT DISTINCT ON(k) id,payload FROM events ORDER BY k,stamp DESC,id DESC", []string{"id", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "new1b"}, {int64(5), "new2"}, {int64(8), "freshnull"}}))
		for _, w := range witnesses {
			materializedViewPopulation(run, w.name, w.populated)
			if w.populated {
				queryDistinctBoth(run, w.step)
			} else {
				queryDistinctBoth(run, materializedViewUnpopulated(w.name))
			}
		}
		if !b.pg {
			queryDistinctOnMaterializedNativeReplay(t, p, witnesses)
		}
	})
}

func TestReferenceQueryDistinctOnMaterializedPolicies(t *testing.T) {
	queryDistinctOnScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		queryDistinctOnEvents(run)
		run(referenceExec("CREATE MATERIALIZED VIEW mv_on_sentinel AS SELECT DISTINCT ON(1) 17 AS sentinel FROM events ORDER BY 1", 1))
		sentinel := referenceSelect("SELECT sentinel FROM mv_on_sentinel", []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(17)}})
		for _, suffix := range []string{"", " WITH NO DATA"} {
			parameter := referencePrepared(referenceErrorDiagnostic("CREATE MATERIALIZED VIEW mv_on_parameter AS SELECT DISTINCT ON($1::integer) id FROM events"+suffix, controlsReferenceDiagnostic("0A000", "materialized views may not be defined using bound parameters"), 7))
			parameter.errorStage = "prepare"
			run(parameter)
			queryTableCreateAbsent(t, run, b.pg, "mv_on_parameter")
			run(sentinel)
		}
		failing := "SELECT DISTINCT ON(k) 1/(id-1) AS payload FROM events ORDER BY k,id DESC"
		for _, prepared := range []bool{false, true} {
			failure := referenceImplicitError("CREATE MATERIALIZED VIEW mv_on_failed AS "+failing, referenceDiagnostic{Code: "22012", Message: "division by zero"})
			failure.prepared = prepared
			failure.errorStage = "execute"
			run(failure)
			queryTableCreateAbsent(t, run, b.pg, "mv_on_failed")
			run(sentinel)
			materializedViewPopulation(run, "mv_on_sentinel", true)
		}
		run(referenceExec("CREATE MATERIALIZED VIEW mv_on_deferred AS "+failing+" WITH NO DATA", 0))
		queryDistinctBoth(run, materializedViewUnpopulated("mv_on_deferred"))
		materializedViewPopulation(run, "mv_on_deferred", false)
		run(sentinel)
	})
}

func queryDistinctOnMaterializedNativeReplay(t *testing.T, p *referencePair, witnesses []queryDistinctOnMaterializedWitness) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	fresh := newReferenceFixture(t, false, true)
	if err := stuntgres.Restore(p.ctx, fresh.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, fresh.memory, fresh.memoryRecord, s) }
	check := func() {
		for _, w := range witnesses {
			materializedViewPopulation(run, w.name, w.populated)
			if w.populated {
				queryDistinctBoth(run, w.step)
			} else {
				queryDistinctBoth(run, materializedViewUnpopulated(w.name))
			}
		}
	}
	check()
	retained, err := fresh.native.QueryContext(p.ctx, witnesses[0].step.query)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	invalid, want := queryDistinctOnMarked(dump.String()+"/* λ */ CREATE MATERIALIZED VIEW broken_on_mv AS SELECT DISTINCT ON(|k) id FROM events ORDER BY id WITH NO DATA;", "42P10", "SELECT DISTINCT ON expressions must match initial ORDER BY expressions")
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	referenceAssertDiagnostic(t, stuntgres.Restore(p.ctx, fresh.native, bytes.NewBufferString(invalid)), &want)
	check()
	canceled, cancel := context.WithCancel(p.ctx)
	cancel()
	if err := stuntgres.Restore(canceled, fresh.native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	check()
	if err := stuntgres.Reset(p.ctx, fresh.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, fresh.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	first := witnesses[0].step
	stringCoreRetainedRows(t, retained, nil, first.columns, first.types, first.rows, first.bag)
	check()
	// PG consumes the emitted definitions, not native snapshot comment records.
	// Its restored materialized definitions intentionally remain WITH NO DATA.
	if p.postgres != nil {
		tx, err := p.postgres.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatal("emitted materialized ON definition replay", err)
		}
		for _, w := range witnesses {
			if _, err := tx.ExecContext(p.ctx, "SAVEPOINT unpopulated_on"); err != nil {
				t.Fatal(err)
			}
			rows, err := tx.QueryContext(p.ctx, "SELECT * FROM "+w.name)
			if rows != nil {
				rows.Close()
				t.Fatal("unpopulated emitted definition returned rows", w.name)
			}
			expected := controlsReferenceDiagnostic("55000", "materialized view \""+w.name+"\" has not been populated")
			expected.Hint = "Use the REFRESH MATERIALIZED VIEW command."
			referenceAssertDiagnostic(t, err, &expected)
			if _, err := tx.ExecContext(p.ctx, "ROLLBACK TO SAVEPOINT unpopulated_on"); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.ExecContext(p.ctx, "RELEASE SAVEPOINT unpopulated_on"); err != nil {
				t.Fatal(err)
			}
		}
	}
}
