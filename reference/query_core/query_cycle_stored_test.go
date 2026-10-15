package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceQueryCycleStored(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "cycle_store_schema")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := cycleConn(t, p, backend)
			run := subqueryRun(t, p, backend, c)
			cycleSeed(run)
			for _, kind := range []struct{ name, ddl string }{
				{"view", "CREATE VIEW cycle_pseudo_view AS "},
				{"ctas", "CREATE TABLE cycle_pseudo_ctas AS "},
				{"materialized view", "CREATE MATERIALIZED VIEW cycle_pseudo_mv AS "},
			} {
				t.Run("persistent pseudo-type "+kind.name, func(t *testing.T) {
					q := "/*é*/ " + kind.ddl + cycleGraph + "SELECT path FROM r"
					cycleAssertDiagnostic(t, p, backend, c, q, fixture.ControlsReferenceDiagnostic("42P16", `column "path" has pseudo-type record[]`), true)
				})
			}
			run(cteBag("SELECT relname FROM pg_catalog.pg_class WHERE relname IN ('cycle_pseudo_view','cycle_pseudo_ctas','cycle_pseudo_mv')", []string{"relname"}, []string{"NAME"}, nil))
			body := cycleGraph + "SELECT n,cyc,path::text AS path FROM r"
			run(fixture.Exec("CREATE VIEW cycle_stored_view AS "+body, 0))
			fixture.ViewCopy(t, p, backend, "cycle_stored_view", "cycle_stored_copy")
			run(fixture.Exec("CREATE SCHEMA cycle_store_schema", 0))
			run(fixture.Exec("ALTER VIEW cycle_stored_copy RENAME TO cycle_stored_renamed", 0))
			run(fixture.Exec("ALTER VIEW cycle_stored_renamed SET SCHEMA cycle_store_schema", 0))
			run(fixture.Exec("CREATE MATERIALIZED VIEW cycle_stored_mv AS SELECT * FROM cycle_stored_view", 4))
			run(fixture.Exec("CREATE TABLE cycle_stored_ctas AS "+body, 4))
			run(fixture.Exec("CREATE TABLE cycle_stored_sink(n TEXT,cyc BOOL,path TEXT)", 0))
			run(fixture.Exec("INSERT INTO cycle_stored_sink "+body, 4))
			// Typed custom constants and quoted generated identifiers survive
			// symbolic view rendering without exposing internal generated slots.
			run(fixture.Exec(`CREATE VIEW cycle_stored_marks AS WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n FROM r) CYCLE n SET "Mark" TO bigint '1' DEFAULT bigint '0' USING "Path" SELECT n,"Mark","Path"::text AS path FROM r`, 0))
			check := func(t *testing.T, want [][]any) {
				t.Helper()
				for _, name := range []string{"cycle_stored_view", "cycle_store_schema.cycle_stored_renamed"} {
					subqueryBoth(subqueryRun(t, p, backend, c), cteBag("SELECT * FROM "+name, []string{"n", "cyc", "path"}, []string{"TEXT", "BOOL", "TEXT"}, want))
				}
			}
			check(t, cycleGraphRows())
			for _, name := range []string{"cycle_stored_mv", "cycle_stored_ctas", "cycle_stored_sink"} {
				subqueryBoth(run, cteBag("SELECT * FROM "+name, []string{"n", "cyc", "path"}, []string{"TEXT", "BOOL", "TEXT"}, cycleGraphRows()))
			}
			run(fixture.Exec("ALTER TABLE cycle_edges RENAME COLUMN dst TO target", 0))
			run(fixture.Exec("INSERT INTO cycle_edges VALUES('c','d')", 1))
			live := append(cycleGraphRows(), []any{"d", false, "{(a),(c),(d)}"})
			check(t, live)
			run(cteBag("SELECT * FROM cycle_stored_mv", []string{"n", "cyc", "path"}, []string{"TEXT", "BOOL", "TEXT"}, cycleGraphRows()))
			subqueryBoth(run, fixture.Exec("REFRESH MATERIALIZED VIEW cycle_stored_mv", 0))
			run(cteBag("SELECT * FROM cycle_stored_mv", []string{"n", "cyc", "path"}, []string{"TEXT", "BOOL", "TEXT"}, live))
			run(cteBag(`SELECT * FROM cycle_stored_marks`, []string{"n", "Mark", "path"}, []string{"INT4", "INT8", "TEXT"}, [][]any{{int64(1), int64(0), "{(1)}"}, {int64(1), int64(1), "{(1),(1)}"}}))
			t.Run("staged failure preservation", func(t *testing.T) { cycleRestoreFailures(t, p, backend, c) })
			if !backend.PG {
				cycleStoredReplay(t, p, backend, c, live)
			}
		})
	}
}

func cycleRestoreFailures(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := subqueryRun(t, p, b, c)
	run(fixture.Exec("CREATE TABLE cycle_restore_sentinel(n INT)", 0))
	run(fixture.Exec("INSERT INTO cycle_restore_sentinel VALUES(99)", 1))
	prefix := "CREATE TABLE cycle_restore_staged(n INT); "
	badView := "CREATE VIEW cycle_restore_bad AS WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n FROM r) CYCLE missing SET cyc USING path SELECT n FROM r;"
	for _, tc := range []struct {
		name, script string
		want         fixture.Diagnostic
	}{
		{"later CYCLE binding", prefix + badView, cycleDiagnostic("42601", `cycle column "missing" not in WITH query column list`, prefix+badView, "CYCLE")},
		{"malformed", prefix + "CREATE VIEW cycle_restore_bad AS SELECT FROM ;", fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near ";"`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Restore parses the complete script once and retains its source
			// positions through staged binding, as PostgreSQL does here.
			if tc.name == "malformed" {
				tc.want.Position = fixture.Position(88)
			}
			var err error
			if b.PG {
				tx, e := c.BeginTx(p.Ctx, nil)
				if e != nil {
					t.Fatal(e)
				}
				defer tx.Rollback()
				_, err = tx.ExecContext(p.Ctx, tc.script)
				if rollback := tx.Rollback(); rollback != nil {
					t.Fatal(rollback)
				}
			} else {
				err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(tc.script))
			}
			if err == nil {
				t.Fatal("failed staged replacement succeeded")
			}
			fixture.AssertDiagnostic(t, err, &tc.want)
			run := subqueryRun(t, p, b, c)
			run(fixture.Select("SELECT n FROM cycle_restore_sentinel", []string{"n"}, []string{"INT4"}, recursiveCTEInts(99)))
			run(cteBag("SELECT relname FROM pg_catalog.pg_class WHERE relname IN ('cycle_restore_staged','cycle_restore_bad')", []string{"relname"}, []string{"NAME"}, nil))
		})
	}
	if b.PG {
		return
	}
	before := fixture.ViewDump(t, p)
	failure := errors.New("cycle replay reader failure")
	err := stuntgres.Restore(p.Ctx, p.Native, io.MultiReader(strings.NewReader(prefix), fixture.CatalogControlFailingReader{Err: failure}))
	fixture.LexicalOrdinary(t, err, failure)
	ctx, cancel := context.WithCancel(p.Ctx)
	defer cancel()
	err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
	fixture.LexicalOrdinary(t, err, context.Canceled)
	if after := fixture.ViewDump(t, p); !bytes.Equal(before, after) {
		t.Fatal("failed restore changed persistent catalog/data/metadata")
	}
	run(fixture.Select("SELECT n FROM cycle_restore_sentinel", []string{"n"}, []string{"INT4"}, recursiveCTEInts(99)))
}

func cycleStoredReplay(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, live [][]any) {
	t.Helper()
	dump := fixture.ViewDump(t, p)
	// Retain a raw generated path while physical graph data and catalogs change.
	q := strings.Replace(cycleGraph, "e.dst", "e.target", 1) + "SELECT * FROM r"
	rows, err := c.QueryContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	metadata := cycleMetadata(b, []string{"n", "cyc", "path"}, []string{"TEXT", "BOOL", "_RECORD"})
	metadata(t, rows)
	subqueryRun(t, p, b, c)(fixture.Exec("INSERT INTO cycle_edges VALUES('d','e')", 1))
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	fixture.ViewRestore(t, p, dump)
	metadata(t, rows)
	var retained [][]any
	for rows.Next() {
		var n, cyc, path any
		if err := rows.Scan(&n, &cyc, &path); err != nil {
			t.Fatal(err)
		}
		retained = append(retained, []any{n, cyc, path})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	cycleAssertBag(t, retained, live)
	checks := []fixture.Step{
		cteBag("SELECT * FROM cycle_stored_view", []string{"n", "cyc", "path"}, []string{"TEXT", "BOOL", "TEXT"}, live),
		cteBag("SELECT * FROM cycle_store_schema.cycle_stored_renamed", []string{"n", "cyc", "path"}, []string{"TEXT", "BOOL", "TEXT"}, live),
		cteBag("SELECT * FROM cycle_stored_mv", []string{"n", "cyc", "path"}, []string{"TEXT", "BOOL", "TEXT"}, live),
		cteBag("SELECT * FROM cycle_stored_ctas", []string{"n", "cyc", "path"}, []string{"TEXT", "BOOL", "TEXT"}, cycleGraphRows()),
		cteBag("SELECT * FROM cycle_stored_sink", []string{"n", "cyc", "path"}, []string{"TEXT", "BOOL", "TEXT"}, cycleGraphRows()),
		cteBag(`SELECT * FROM cycle_stored_marks`, []string{"n", "Mark", "path"}, []string{"INT4", "INT8", "TEXT"}, [][]any{{int64(1), int64(0), "{(1)}"}, {int64(1), int64(1), "{(1),(1)}"}}),
	}
	for _, s := range checks {
		subqueryBoth(subqueryRun(t, p, b, c), s)
	}
	foreign := fixture.NewFixture(t, false, false)
	fixture.ViewRestore(t, foreign, dump)
	for _, fb := range foreign.Backends() {
		for _, s := range checks {
			fixture.RunStep(t, foreign.Ctx, fb.DB, fb.Recorder, s)
		}
	}
	if redump := fixture.ViewDump(t, p); !bytes.Equal(dump, redump) {
		t.Fatal("symbolic CYCLE replay changed dump")
	}
	if p.Postgres != nil {
		// Replay actual native output, not independently handwritten equivalent
		// SQL. Native is visited first; clean these names for the PG callback.
		defer func() {
			for _, q := range []string{"DROP SCHEMA IF EXISTS cycle_store_schema CASCADE", "DROP VIEW IF EXISTS cycle_stored_view CASCADE", "DROP VIEW IF EXISTS cycle_stored_marks CASCADE", "DROP TABLE IF EXISTS cycle_edges CASCADE", "DROP TABLE IF EXISTS cycle_stored_ctas CASCADE", "DROP TABLE IF EXISTS cycle_stored_sink CASCADE", "DROP TABLE IF EXISTS cycle_restore_sentinel CASCADE"} {
				if _, err := p.Postgres.ExecContext(p.Ctx, q); err != nil {
					t.Error(err)
				}
			}
		}()
		if _, err := p.Postgres.ExecContext(p.Ctx, string(dump)); err != nil {
			t.Fatal("actual native CYCLE dump PostgreSQL replay", err)
		}
		// Native snapshot comments preserve MV rows through native Restore,
		// but PostgreSQL executes the emitted WITH NO DATA definition only.
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.MaterializedViewUnpopulated("cycle_stored_mv"))
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("REFRESH MATERIALIZED VIEW cycle_stored_mv", 0))
		for _, s := range checks {
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Prepared(s))
		}
	}
}
